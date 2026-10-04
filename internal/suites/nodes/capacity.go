package nodes

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/harvester/hvperf/internal/suites/options"
	"github.com/harvester/hvperf/pkg/k8s"
	"github.com/harvester/hvperf/pkg/suites"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
)

var _ suites.Suite = &NodeCapacitySuite{}

func init() {
	suites.Register(NewNodeCapacitySuite())
}

// NodeCapacitySuite implements test suite to assess node resource capacity.
type NodeCapacitySuite struct {
	suites.SuiteMarshaler
	*suites.ProgressReporter
	*suites.Clients
}

// NewNodeCapacitySuite creates a new instance of NodeCapacitySuite.
func NewNodeCapacitySuite() *NodeCapacitySuite {
	s := &NodeCapacitySuite{}
	s.Marshal = s
	return s
}

func (s *NodeCapacitySuite) Name() string {
	return "node-capacity"
}

func (s *NodeCapacitySuite) Description() string {
	return "assess the node resource capacity of the cluster"
}

func (s *NodeCapacitySuite) IsReadWrite() bool {
	return true
}

func (s *NodeCapacitySuite) RunE(ctx context.Context, runID, namespace string, opts suites.Options) suites.SuiteResult {
	cases := []struct {
		caseName string
		caseFunc func(context.Context, string) *suites.CaseResult
	}{
		{
			caseName: "node os info",
			caseFunc: func(ctx context.Context, caseName string) *suites.CaseResult {
				return s.execNodeOSInfo(ctx, caseName)
			},
		},
		{
			caseName: "node disk info",
			caseFunc: func(ctx context.Context, caseName string) *suites.CaseResult {
				o, err := CapacityOptionsDefaults(opts)
				if err != nil {
					return suites.NewCaseResultErrored(caseName, time.Now(), time.Now(), err)
				}

				return s.execNodeDiskInfo(
					ctx,
					caseName,
					runID,
					namespace,
					o,
				)
			},
		},
	}

	var caseResults []*suites.CaseResult
	for _, c := range cases {
		klog.V(3).InfoS("executing test case", "suite", s.Name(), "name", c.caseName)
		s.CaseStart(s.Name(), c.caseName)
		caseResult := c.caseFunc(ctx, c.caseName)
		caseResults = append(caseResults, caseResult)
		s.CaseDone(s.Name(), c.caseName, caseResult.State, time.Since(caseResult.DateTimeStart))
	}

	return suites.SuiteResult{
		Name:    s.Name(),
		RunID:   runID,
		Results: caseResults,
	}
}

func (s *NodeCapacitySuite) execNodeOSInfo(ctx context.Context, name string) *suites.CaseResult {
	nodes, err := s.K8sClientSet.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return suites.NewCaseResultErrored(name, time.Now(), time.Now(), fmt.Errorf("failed to list nodes: %w", err))
	}

	var results []*suites.K8sResourceResult
	start := time.Now()
	for _, node := range nodes.Items {
		klog.V(3).InfoS("collecting node resource info", "node", node.GetName(), "suite", s.Name(), "case", name)
		result := &suites.K8sResourceResult{
			Resource: "node",
			Subject:  node.GetName(),
			Data:     map[string]string{},
		}

		pods, err := s.K8sClientSet.CoreV1().Pods("").List(ctx, metav1.ListOptions{
			FieldSelector: "spec.nodeName=" + node.GetName() + ",status.phase=Running",
		})
		if err != nil {
			result.Err = err.Error()
			results = append(results, result)
			continue
		}

		status := node.Status
		nodeInfo := status.NodeInfo
		result.Data["architecture"] = nodeInfo.Architecture
		result.Data["container.runtime"] = nodeInfo.ContainerRuntimeVersion
		result.Data["cpu.allocatable"] = status.Allocatable.Cpu().String()
		result.Data["cpu.capacity"] = status.Capacity.Cpu().String()
		result.Data["ephemeral-storage.allocatable"] = status.Allocatable.StorageEphemeral().String()
		result.Data["ephemeral-storage.capacity"] = status.Capacity.StorageEphemeral().String()
		result.Data["kernel.version"] = nodeInfo.KernelVersion
		result.Data["kubelet.version"] = nodeInfo.KubeletVersion
		result.Data["memory.allocatable"] = status.Allocatable.Memory().String()
		result.Data["memory.capacity"] = status.Capacity.Memory().String()
		result.Data["os.image"] = nodeInfo.OSImage
		result.Data["pods.capacity"] = status.Capacity.Pods().String()
		result.Data["pods.running"] = strconv.Itoa(len(pods.Items))

		results = append(results, result)

	}

	caseResult := suites.NewCaseResult(name, start, time.Now(), nil, nil)
	caseResult = caseResult.WithK8sResourceResults(results)
	return caseResult
}

func (s *NodeCapacitySuite) execNodeDiskInfo(
	ctx context.Context,
	caseName string,
	runID string,
	namespace string,
	opts *CapacityOptions,
) *suites.CaseResult {
	if _, err := k8s.EnsureNamespace(ctx, s.Clients, namespace, opts.NamespaceReadyTimeout); err != nil {
		return suites.NewCaseResultErrored(caseName, time.Now(), time.Now(), fmt.Errorf("failed to ensure namespace '%s' is ready: %w", namespace, err))
	}
	klog.V(3).InfoS("namespace is ready", "name", namespace, "suite", s.Name(), "case", caseName)

	ds, pods, cleanup, err := k8s.EnsureDaemonSetReady(
		ctx,
		s.Clients,
		s.Name(),
		runID,
		namespace,
		opts.PodImageName+":"+opts.PodImageTag,
		opts.PodActiveDeadline,
		opts.PodReadyTimeout,
	)
	defer func() {
		if cleanup != nil {
			if err := cleanup(); err != nil {
				klog.ErrorS(err, "failed to clean up daemonset", "suite", s.Name(), "case", caseName)
			}
		}
	}()
	if err != nil {
		return suites.NewCaseResultErrored(caseName, time.Now(), time.Now(), err)
	}
	klog.V(3).InfoS("daemonset is ready", "name", ds.Name, "readyPodCount", ds.Status.NumberReady, "suite", s.Name(), "case", caseName)

	var cmdResults []*suites.CmdResult
	start := time.Now()
	cmds := [][]string{
		{"nsenter", "--target", "1", "--mount", "--", "lsblk", "-o", "NAME,KNAME,SIZE,FSTYPE,LABEL,MOUNTPOINT,TYPE,PKNAME"},
	}
	for _, pod := range pods {
		for _, cmd := range cmds {
			klog.V(5).InfoS("exec", "cmd", strings.Join(cmd, " "), "pod", pod.Name, "suite", s.Name(), "case", caseName)
			out, err := k8s.ExecPod(ctx, s.Clients, pod, cmd)
			cmdResult := &suites.CmdResult{
				Cmd:    strings.Join(cmd, " "),
				Node:   pod.Spec.NodeName,
				Stdout: out.Stdout,
				Stderr: out.Stderr,
			}
			if err != nil {
				cmdResult.Err = err.Error()
			}
			cmdResults = append(cmdResults, cmdResult)
		}
	}

	objs := []runtime.Object{ds}
	for _, pod := range pods {
		objs = append(objs, pod)
	}
	return suites.NewCaseResult(caseName, start, time.Now(), cmdResults, nil, objs...)
}

func (s *NodeCapacitySuite) SetClients(clientSets *suites.Clients) {
	s.Clients = clientSets
}

func (s *NodeCapacitySuite) SetProgressReporter(reporter *suites.ProgressReporter) {
	s.ProgressReporter = reporter
}

type CapacityOptions struct {
	NamespaceReadyTimeout time.Duration
	PodActiveDeadline     time.Duration
	PodImageName          string
	PodImageTag           string
	PodReadyTimeout       time.Duration
}

func CapacityOptionsDefaults(opts suites.Options) (*CapacityOptions, error) {
	sysOpts, err := options.FromOptions[*CapacityOptions](&opts)
	if err != nil {
		return nil, err
	}

	return &CapacityOptions{
		NamespaceReadyTimeout: sysOpts.NamespaceReadyTimeout,
		PodActiveDeadline:     sysOpts.PodActiveDeadline,
		PodImageName:          sysOpts.PodImageName,
		PodImageTag:           sysOpts.PodImageTag,
		PodReadyTimeout:       sysOpts.PodReadyTimeout,
	}, nil
}
