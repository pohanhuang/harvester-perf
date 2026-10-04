package etcd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/harvester/hvperf/internal/suites/options"
	"github.com/harvester/hvperf/pkg/k8s"
	prom "github.com/harvester/hvperf/pkg/prometheus"
	pkgsuites "github.com/harvester/hvperf/pkg/suites"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
)

func init() {
	pkgsuites.Register(NewBenchmarkSuite())
}

var _ pkgsuites.Suite = &BenchmarkSuite{}

// BenchmarkSuite implements test suite to assess etcd performance.
type BenchmarkSuite struct {
	pkgsuites.SuiteMarshaler
	*pkgsuites.ProgressReporter
	*pkgsuites.Clients
}

// NewBenchmarkSuite creates a new instance of EtcdBenchmarkSuite with the
// provided options.
func NewBenchmarkSuite() *BenchmarkSuite {
	s := &BenchmarkSuite{}
	s.Marshal = s
	return s
}

func (s *BenchmarkSuite) Name() string {
	return "etcd-benchmark"
}

func (s *BenchmarkSuite) Description() string {
	return "run the etcd-benchmark tool to against the cluster's etcd"
}

func (s *BenchmarkSuite) IsReadWrite() bool {
	return true
}

func (s *BenchmarkSuite) RunE(
	ctx context.Context,
	runID string,
	namespace string,
	opts pkgsuites.Options,
) pkgsuites.SuiteResult {
	o, err := BenchmarkOptionsDefaults(opts)
	if err != nil {
		return pkgsuites.SuiteResult{
			Name:  s.Name(),
			RunID: runID,
			Err:   err.Error(),
		}
	}

	// TODO: merge custom options
	// custom, err := FromOptions(opts)
	// if err != nil {
	//      return pkgsuites.SuiteResult{}, err
	// }

	etcd, etcdReady, err := k8s.EnsureEtcdReady(ctx, s.Clients, o.EtcdNamespace, o.EtcdReadyTimeout)
	if err != nil {
		return pkgsuites.SuiteResult{
			Name:  s.Name(),
			RunID: runID,
			Err:   fmt.Sprintf("etcd not ready: %v", err.Error()),
		}
	}
	if !etcdReady {
		return pkgsuites.SuiteResult{
			Name:  s.Name(),
			RunID: runID,
			Err:   "etcd not ready",
		}
	}
	klog.V(3).InfoS("etcd pods are ready", "namespace", o.EtcdNamespace, "count", len(etcd.Items))

	if _, err := k8s.EnsureNamespace(ctx, s.Clients, namespace, o.NamespaceReadyTimeout); err != nil {
		return pkgsuites.SuiteResult{
			Name:  s.Name(),
			RunID: runID,
			Err:   fmt.Sprintf("namespace %s not ready: %v", namespace, err.Error()),
		}
	}
	klog.V(3).Infof("namespace:'%s' is now ready\n", namespace)

	// ensure the job is created and ready
	_, pod, err := k8s.EnsureJobReady(ctx, s.Clients,
		s.Name(),
		runID,
		namespace,
		o.JobPodNode,
		o.JobPodImageName+":"+o.JobPodImageTag,
		o.JobActiveDeadline,
		o.JobPodReadyTimeout,
		o.JobPodTTLAfterFinished,
		o.JobSuspend)
	if err != nil {
		return pkgsuites.SuiteResult{
			Name:  s.Name(),
			RunID: runID,
			Err:   fmt.Sprintf("job not ready: %v", err.Error()),
		}
	}
	klog.V(3).Infof("pod:'%s' is now ready, phase:'%s'\n", pod.GetName(), pod.Status.Phase)

	// copy the etcdctl and benchmark binaries to the job pod. the job pod has a
	// host mount to /var/lib/rancher, where the etcd tls certs are stored.
	klog.V(3).Infof("copying '%s' to pod '%s'\n",
		fmt.Sprintf("%s,%s", o.EtcdctlLocalPath, o.EtcdBenchmarkLocalPath),
		pod.GetName())
	if err := k8s.CopyToJobPod(
		ctx,
		s.Clients,
		pod,
		o.EtcdRemoteCopyTargetDir,
		o.EtcdctlLocalPath,
		o.EtcdBenchmarkLocalPath,
		o.PromtoolLocalPath,
	); err != nil {
		return pkgsuites.SuiteResult{
			Name:  s.Name(),
			RunID: runID,
			Err:   fmt.Sprintf("failed to copy tools to pod job %s/%s: %v", pod.GetNamespace(), pod.GetName(), err.Error()),
		}
	}

	var caseResults []*pkgsuites.CaseResult
	cases := []struct {
		caseName     string
		caseFn       func(context.Context, string, *corev1.Pod, *BenchmarkOptions, ...string) *pkgsuites.CaseResult
		optsOverride func() *BenchmarkOptions
	}{
		{
			caseName:     "etcd healthcheck",
			caseFn:       s.execHealthcheck,
			optsOverride: func() *BenchmarkOptions { return o },
		},
		{
			caseName:     "etcd check perf",
			caseFn:       s.execCheckPerf,
			optsOverride: func() *BenchmarkOptions { return o },
		},
		{
			caseName:     "etcd benchmark (serial)",
			caseFn:       s.execBenchmark,
			optsOverride: func() *BenchmarkOptions { return o },
		},
		{
			caseName: "etcd benchmark (concurrent)",
			caseFn:   s.execBenchmark,
			optsOverride: func() *BenchmarkOptions {
				o.PutLoadSize = DefaultConcurrentLoadSize
				o.GRPCClientCount = DefaultConcurrentClientCount
				o.GRPCConnCount = DefaultConcurrentConnCount
				return o
			},
		},
	}

	for _, c := range cases {
		klog.V(3).Infof("running %s in pod '%s'\n", c.caseName, pod.GetName())
		s.CaseStart(s.Name(), c.caseName)
		caseResult := c.caseFn(ctx, c.caseName, pod, c.optsOverride(), s.args(c.optsOverride())...)
		caseResults = append(caseResults, caseResult)
		s.CaseDone(s.Name(), c.caseName, caseResult.State, time.Since(caseResult.DateTimeStart))
	}

	klog.V(3).Infof("running etcd monitoring (promql) in pod '%s'\n", pod.GetName())
	caseName := "etcd monitoring (promql)"
	s.CaseStart(s.Name(), caseName)
	caseResult := s.monitoring(ctx, caseName, pod, len(etcd.Items), o)
	caseResults = append(caseResults, caseResult)
	s.CaseDone(s.Name(), caseName, caseResult.State, time.Since(caseResult.DateTimeStart))

	suiteParams, err := pkgsuites.ToSuiteParams(o)
	if err != nil {
		// just log the params conversion error, don't fail the suite. the only effect
		// is that the suite params won't be reported in the results.
		klog.V(3).ErrorS(err, "failed to convert suite options to suite params\n")
	}
	return pkgsuites.SuiteResult{
		Name:    s.Name(),
		Params:  suiteParams,
		RunID:   runID,
		Results: caseResults,
	}
}

func (s *BenchmarkSuite) args(opts *BenchmarkOptions) []string {
	endpointsArgs := []string{
		"--endpoints", opts.EtcdEndpoints,
	}
	tlsArgs := []string{
		"--cacert", fmt.Sprintf("%s/server-ca.crt", opts.EtcdRemoteTLSCertDir),
		"--cert", fmt.Sprintf("%s/server-client.crt", opts.EtcdRemoteTLSCertDir),
		"--key", fmt.Sprintf("%s/server-client.key", opts.EtcdRemoteTLSCertDir),
	}
	return append(endpointsArgs, tlsArgs...)
}

func (s *BenchmarkSuite) execHealthcheck(
	ctx context.Context,
	name string,
	pod *corev1.Pod,
	opts *BenchmarkOptions,
	args ...string,
) *pkgsuites.CaseResult {
	outArgs := []string{
		"-w", opts.EtcdctlOutputFormat,
	}
	cmds := [][]string{
		{"etcdctl", "endpoint", "status"},
		{"etcdctl", "endpoint", "health"},
		{"etcdctl", "member", "list"},
	}

	var cmdResults []*pkgsuites.CmdResult
	start := time.Now()
	args = append(args, outArgs...)
	for _, cmd := range cmds {
		cmd = append(cmd, args...)
		out, err := k8s.ExecPod(ctx, s.Clients, pod, cmd)
		cmdResult := &pkgsuites.CmdResult{
			Cmd:    strings.Join(cmd, " "),
			Stdout: out.Stdout,
			Stderr: out.Stderr,
		}
		if err != nil {
			cmdResult.Err = err.Error()
		}
		cmdResults = append(cmdResults, cmdResult)
	}
	return pkgsuites.NewCaseResult(name, start, time.Now(), cmdResults, nil, pod)
}

func (s *BenchmarkSuite) execCheckPerf(
	ctx context.Context,
	name string,
	pod *corev1.Pod,
	opts *BenchmarkOptions,
	args ...string,
) *pkgsuites.CaseResult {
	outArgs := []string{
		"-w", opts.EtcdctlOutputFormat,
		"--load", opts.CheckPerfLoadSize,
	}
	cmds := [][]string{
		{"etcdctl", "check", "perf"},
	}

	var cmdResults []*pkgsuites.CmdResult
	start := time.Now()
	args = append(args, outArgs...)
	for _, cmd := range cmds {
		cmd = append(cmd, args...)
		out, err := k8s.ExecPod(ctx, s.Clients, pod, cmd)
		cmdResult := &pkgsuites.CmdResult{
			Cmd:    strings.Join(cmd, " "),
			Stdout: out.Stdout,
			Stderr: out.Stderr,
		}
		if err != nil {
			cmdResult.Err = err.Error()
		}
		cmdResults = append(cmdResults, cmdResult)
	}

	return pkgsuites.NewCaseResult(name, start, time.Now(), cmdResults, nil, pod)
}

func (s *BenchmarkSuite) execBenchmark(
	ctx context.Context,
	name string,
	pod *corev1.Pod,
	opts *BenchmarkOptions,
	args ...string,
) *pkgsuites.CaseResult {
	cmds := [][]string{
		{
			// write serial
			"benchmark",
			"--conns", fmt.Sprintf("%v", opts.GRPCConnCount),
			"--clients", fmt.Sprintf("%v", opts.GRPCClientCount),
			"put",
			"--key-size", fmt.Sprintf("%v", opts.PutKeySize),
			"--sequential-keys",
			"--total", fmt.Sprintf("%v", opts.PutLoadSize),
			"--val-size", fmt.Sprintf("%v", opts.PutValSize),
		},
		{
			// read linearizable
			"benchmark",
			"--conns", fmt.Sprintf("%v", opts.GRPCConnCount),
			"--clients", fmt.Sprintf("%v", opts.GRPCClientCount),
			"range",
			"hvperf-probe",
			"--consistency", DefaultRangeConsistencyLinearizable,
			"--total", fmt.Sprintf("%v", opts.PutLoadSize),
		},
		{
			// read serializable
			"benchmark",
			"--conns", fmt.Sprintf("%v", opts.GRPCConnCount),
			"--clients", fmt.Sprintf("%v", opts.GRPCClientCount),
			"range",
			"hvperf-probe",
			"--consistency", DefaultRangeConsistencySerializable,
			"--total", fmt.Sprintf("%v", opts.PutLoadSize),
		},
	}

	var cmdResults []*pkgsuites.CmdResult
	start := time.Now()
	for _, cmd := range cmds {
		cmd = append(cmd, args...)
		out, err := k8s.ExecPod(ctx, s.Clients, pod, cmd)
		cmdResult := &pkgsuites.CmdResult{
			Cmd:    strings.Join(cmd, " "),
			Stdout: out.Stdout,
			Stderr: out.Stderr,
		}
		if err != nil {
			cmdResult.Err = err.Error()
		}
		cmdResults = append(cmdResults, cmdResult)
	}

	return pkgsuites.NewCaseResult(name, start, time.Now(), cmdResults, nil, pod)
}

func (s *BenchmarkSuite) monitoring(
	ctx context.Context,
	name string,
	pod *corev1.Pod,
	etcdCount int,
	opts *BenchmarkOptions,
) *pkgsuites.CaseResult {
	// check if monitoring addon is enabled and ready. if not, skip the promql
	// execution.
	start := time.Now()
	ready, err := k8s.MonitoringEnabled(ctx, s.Clients, opts.MonitoringNamespace, opts.MonitoringAddonName)
	if err != nil {
		err = fmt.Errorf("failed to check if monitoring addon is enabled, skipping promql execution: %w", err)
		return pkgsuites.NewCaseResultSkipped(name, start, time.Now(), err)
	}

	// skip if not ready
	if !ready {
		err = fmt.Errorf("monitoring add-on is not ready, skipping promql execution")
		return pkgsuites.NewCaseResultSkipped(name, start, time.Now(), err)
	}

	// etcd metrics are not exposed by default, so we need to ensure that the pod
	// monitor is created
	podMonOpts := &k8s.PodMonitorOption{
		Name:            s.Name(),
		Namespace:       pod.GetNamespace(),
		EndpointScheme:  opts.EtcdMetricsScheme,
		EtcdCount:       etcdCount,
		LabelSelector:   k8s.EtcdLabelSelector.MatchLabels,
		MetricsPortName: opts.EtcdMetricsPortName,
		MetricsPath:     opts.EtcdMetricsPath,
		RangeDuration:   opts.MonitoringRangeDuration,
		TargetNamespace: opts.EtcdNamespace,
		WaitTimeout:     opts.MonitoringWaitPodMonitorTimeout,
	}
	cleanup, err := k8s.EnsurePodMonitor(
		ctx,
		s.Clients,
		podMonOpts,
		pod,
	)
	defer func() {
		klog.V(3).InfoS("cleaning up pod monitor", "name", podMonOpts.Name, "namespace", podMonOpts.Namespace)
		if err := cleanup(); err != nil {
			klog.V(3).ErrorS(err, "failed to cleanup pod monitor", "name", podMonOpts.Name, "namespace", podMonOpts.Namespace)
		}
	}()
	if err != nil {
		err = fmt.Errorf("failed to ensure pod monitor: %w", err)
		return pkgsuites.NewCaseResultSkipped(name, start, time.Now(), err)
	}
	klog.V(3).InfoS("pod monitor is ready", "name", podMonOpts.Name, "namespace", podMonOpts.Namespace)

	metricResults := s.execPromQL(ctx, opts)
	return pkgsuites.NewCaseResult(name, start, time.Now(), nil, metricResults, pod)
}

func (s *BenchmarkSuite) execPromQL(
	ctx context.Context,
	opts *BenchmarkOptions,
) []*pkgsuites.MetricResult {
	queries := []string{
		// p99 WAL fsync
		fmt.Sprintf(`histogram_quantile(0.99,sum by (le, pod) (rate(etcd_disk_wal_fsync_duration_seconds_bucket{namespace='%s'}[%s])))`, opts.EtcdNamespace, opts.MonitoringRangeDuration),

		// p99 backend commit
		fmt.Sprintf(`histogram_quantile(0.99,sum by (le, pod) (rate(etcd_disk_backend_commit_duration_seconds_bucket{namespace='%s'}[%s])))`, opts.EtcdNamespace, opts.MonitoringRangeDuration),

		// rate of WAL write bytes
		fmt.Sprintf(`sum by (pod) (rate(etcd_disk_wal_write_bytes_total{namespace='%s'}[%s]))`, opts.EtcdNamespace, opts.MonitoringRangeDuration),

		// p99 peer round-trip time
		// query for ROUND_TRIPPER_RAFT_MESSAGE connection type to fetch small
		// heartbeat/consensus traffic which dictates election-timeout risk
		fmt.Sprintf(`histogram_quantile(0.99,sum by (le, pod, To) (rate(etcd_network_peer_round_trip_time_seconds_bucket{namespace='%s', ConnectionType='ROUND_TRIPPER_RAFT_MESSAGE'}[%s])))`, opts.EtcdNamespace, opts.MonitoringRangeDuration),

		// peer send failure rates for multi-node cluster
		// use the 'To' group-by to obtain the per peer node rates, instead of the
		// cluster-wide rate
		fmt.Sprintf(`sum by (pod, To) (rate(etcd_network_peer_sent_failures_total{namespace="%s"}[%s]))`, opts.EtcdNamespace, opts.MonitoringRangeDuration),

		// peer receive failure rates for multi-node cluster
		// use the 'From' group-by to obtain the per peer node rates, instead of the
		// cluster-wide rate
		fmt.Sprintf(`sum by (pod, From) (rate(etcd_network_peer_received_failures_total{namespace="%s"}[%s]))`, opts.EtcdNamespace, opts.MonitoringRangeDuration),
	}

	var metricResults []*pkgsuites.MetricResult
	for _, query := range queries {
		v, _, err := prom.RunInstant(ctx, s.PromClient, query)
		result := &pkgsuites.MetricResult{
			Query:   query,
			Samples: v,
		}
		if err != nil {
			result.Err = err.Error()
		}
		metricResults = append(metricResults, result)
	}
	return metricResults
}

func (s *BenchmarkSuite) SetClients(clients *pkgsuites.Clients) {
	s.Clients = clients
}

func (s *BenchmarkSuite) SetProgressReporter(progressReporter *pkgsuites.ProgressReporter) {
	s.ProgressReporter = progressReporter
}

type BenchmarkOptions struct {
	EtcdBenchmarkLocalPath string
	EtcdctlLocalPath       string
	PromtoolLocalPath      string

	EtcdctlOutputFormat     string
	EtcdEndpoints           string
	EtcdMetricsPath         string
	EtcdMetricsPortName     string
	EtcdMetricsScheme       string
	MonitoringRangeDuration time.Duration
	EtcdNamespace           string
	EtcdReadyTimeout        time.Duration
	EtcdRemoteTLSCertDir    string
	EtcdRemoteCopyTargetDir string

	JobActiveDeadline      time.Duration
	JobPodContainerName    string
	JobPodImageName        string
	JobPodImageTag         string
	JobPodNode             string
	JobPodTTLAfterFinished time.Duration
	JobPodReadyTimeout     time.Duration
	JobSuspend             bool

	MonitoringAddonName             string
	MonitoringNamespace             string
	MonitoringOutputFormat          string
	MonitoringWaitPodMonitorTimeout time.Duration

	NamespaceReadyTimeout time.Duration

	CheckPerfLoadSize string
	PutLoadSize       uint64
	PutKeySize        uint64
	PutValSize        uint64
	GRPCConnCount     uint64
	GRPCClientCount   uint64
}

// BenchmarkOptionsDefaults returns the default options for the etcd benchmark
// suite. It merges suite-specific options with the default global options for
// the all test suites.
func BenchmarkOptionsDefaults(opts pkgsuites.Options) (*BenchmarkOptions, error) {
	sysOpts, err := options.FromOptions[*BenchmarkOptions](&opts)
	if err != nil {
		return nil, err
	}

	benchmarkOptions := &BenchmarkOptions{
		EtcdBenchmarkLocalPath: "/usr/local/bin/benchmark",
		EtcdctlLocalPath:       "/usr/local/bin/etcdctl",
		PromtoolLocalPath:      "/usr/local/bin/promtool",

		EtcdctlOutputFormat:     "simple",
		EtcdEndpoints:           "https://127.0.0.1:2379",
		EtcdMetricsPath:         "/metrics",
		EtcdMetricsPortName:     "metrics",
		EtcdMetricsScheme:       "http",
		EtcdNamespace:           sysOpts.EtcdNamespace,
		EtcdReadyTimeout:        sysOpts.EtcdReadyTimeout,
		EtcdRemoteCopyTargetDir: "/usr/local/bin/",
		EtcdRemoteTLSCertDir:    "/host/rancher/rke2/server/tls/etcd",

		JobActiveDeadline:      sysOpts.JobActiveDeadline,
		JobPodContainerName:    sysOpts.JobPodContainerName,
		JobPodImageName:        sysOpts.JobPodImageName,
		JobPodImageTag:         sysOpts.JobPodImageTag,
		JobPodNode:             sysOpts.JobPodNode,
		JobPodTTLAfterFinished: sysOpts.JobPodTTLAfterFinished,
		JobPodReadyTimeout:     sysOpts.JobPodReadyTimeout,
		JobSuspend:             sysOpts.JobSuspend,

		MonitoringAddonName:             sysOpts.MonitoringAddonName,
		MonitoringNamespace:             sysOpts.MonitoringNamespace,
		MonitoringRangeDuration:         sysOpts.MonitoringRangeDuration,
		MonitoringOutputFormat:          "promql",
		MonitoringWaitPodMonitorTimeout: sysOpts.MonitoringWaitPodMonitorTimeout,

		NamespaceReadyTimeout: sysOpts.NamespaceReadyTimeout,

		CheckPerfLoadSize: DefaultCheckPerfLoadSize,
		PutLoadSize:       DefaultLoadSize,
		PutKeySize:        DefaultKeySize,
		PutValSize:        DefaultPutValSize,
		GRPCClientCount:   DefaultClientCount,
		GRPCConnCount:     DefaultConnCount,
	}

	return benchmarkOptions, nil
}

const (
	DefaultConcurrentClientCount        = 500
	DefaultConcurrentConnCount          = 100
	DefaultConcurrentLoadSize           = 50000
	DefaultRangeConsistencySerializable = "s"

	DefaultCheckPerfLoadSize            = "s"
	DefaultClientCount                  = 1
	DefaultConnCount                    = 1
	DefaultLoadSize                     = 10000
	DefaultKeySize                      = 8
	DefaultPutValSize                   = 256
	DefaultRangeConsistencyLinearizable = "l"
)
