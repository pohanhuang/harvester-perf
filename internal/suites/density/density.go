package density

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	pkgoptions "github.com/harvester/hvperf/internal/suites/options"
	pkgk8s "github.com/harvester/hvperf/pkg/k8s"
	pkgprom "github.com/harvester/hvperf/pkg/prometheus"
	"github.com/harvester/hvperf/pkg/resource"
	pkgsuites "github.com/harvester/hvperf/pkg/suites"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/kubectl/pkg/util/podutils"
)

const (
	systemNamespaceFilter = `namespace=~"kube-system|kubevirt|cdi|longhorn-system|cattle-monitoring-system|cattle-logging-system|harvester-system|fleet-local"`
	promWindowMin         = time.Minute
	densityRateLimit      = 100 * time.Millisecond
)

func promWindow(elapsed time.Duration) string {
	if elapsed < promWindowMin {
		elapsed = promWindowMin
	}
	return fmt.Sprintf("%ds", int(elapsed.Seconds()))
}

// dynamicQueries returns Prometheus-only resource and VMI Running latency metrics.
func dynamicQueries(w string) []string {
	return []string{
		fmt.Sprintf(`sum by (namespace) (rate(container_cpu_usage_seconds_total{container!="", pod!="", `+systemNamespaceFilter+`}[%s]))`, w),
		fmt.Sprintf(`avg_over_time((sum by (namespace) (container_memory_working_set_bytes{container!="", pod!="", `+systemNamespaceFilter+`}))[%s:30s])`, w),
		fmt.Sprintf(`sum by (node) (rate(container_cpu_usage_seconds_total{container!="", pod!="", `+systemNamespaceFilter+`}[%s]))`, w),
		fmt.Sprintf(`avg_over_time((sum by (node) (container_memory_working_set_bytes{container!="", pod!="", `+systemNamespaceFilter+`}))[%s:30s])`, w),
		fmt.Sprintf(`avg_over_time((sum(rate(container_cpu_usage_seconds_total{namespace="harvester-system",pod=~"virt-api-.*",container!=""}[2m])))[%s:30s])`, w),
		fmt.Sprintf(`avg_over_time((sum(container_memory_working_set_bytes{namespace="harvester-system",pod=~"virt-api-.*",container!=""}))[%s:30s])`, w),
		fmt.Sprintf(`max_over_time((sum(container_memory_working_set_bytes{namespace="harvester-system",pod=~"virt-api-.*",container!=""}))[%s:30s])`, w),
		fmt.Sprintf(`min_over_time((sum(container_memory_working_set_bytes{namespace="harvester-system",pod=~"virt-api-.*",container!=""}))[%s:30s])`, w),
		fmt.Sprintf(`avg_over_time((sum(rate(container_cpu_usage_seconds_total{namespace="harvester-system",pod=~"virt-controller-.*",container!=""}[2m])))[%s:30s])`, w),
		fmt.Sprintf(`avg_over_time((sum(container_memory_working_set_bytes{namespace="harvester-system",pod=~"virt-controller-.*",container!=""}))[%s:30s])`, w),
		fmt.Sprintf(`max_over_time((sum(container_memory_working_set_bytes{namespace="harvester-system",pod=~"virt-controller-.*",container!=""}))[%s:30s])`, w),
		fmt.Sprintf(`min_over_time((sum(container_memory_working_set_bytes{namespace="harvester-system",pod=~"virt-controller-.*",container!=""}))[%s:30s])`, w),
		fmt.Sprintf(`histogram_quantile(0.50, sum by (le) (rate(kubevirt_vmi_phase_transition_time_from_creation_seconds_bucket{phase="Running"}[%s])))`, w),
		fmt.Sprintf(`histogram_quantile(0.95, sum by (le) (rate(kubevirt_vmi_phase_transition_time_from_creation_seconds_bucket{phase="Running"}[%s])))`, w),
		fmt.Sprintf(`histogram_quantile(0.99, sum by (le) (rate(kubevirt_vmi_phase_transition_time_from_creation_seconds_bucket{phase="Running"}[%s])))`, w),
	}
}

var _ pkgsuites.Suite = &DensitySuite{}

func init() {
	suite := &DensitySuite{}
	suite.Marshal = suite
	pkgsuites.Register(suite)
}

type DensitySuite struct {
	pkgsuites.SuiteMarshaler
	*pkgsuites.Clients
}

func (s *DensitySuite) SetProgressReporter(_ *pkgsuites.ProgressReporter) {}
func (s *DensitySuite) Name() string                                      { return "density" }
func (s *DensitySuite) Description() string {
	return "create containerdisk VMIs until the compute-density limit"
}
func (s *DensitySuite) IsReadWrite() bool                     { return true }
func (s *DensitySuite) SetClients(clients *pkgsuites.Clients) { s.Clients = clients }

type densityGlobalOpts struct {
	EtcdNamespace string `json:"EtcdNamespace"`
}

func (s *DensitySuite) RunE(ctx context.Context, runID, namespace string, opts pkgsuites.Options) (result pkgsuites.SuiteResult) {
	result = pkgsuites.SuiteResult{Name: s.Name(), RunID: runID}
	global, err := pkgoptions.FromOptions[densityGlobalOpts](&opts)
	if err != nil {
		result.Err = err.Error()
		return result
	}
	o, err := pkgoptions.DecodeSection[Options](opts, s.Name())
	if err != nil {
		result.Err = err.Error()
		return result
	}
	if err := o.Validate(); err != nil {
		result.Err = err.Error()
		return result
	}
	if params, err := pkgsuites.ToSuiteParams(o); err == nil {
		result.Params = params
	}

	defer s.cleanupRunResult(namespace, runID, &result)
	if err := s.warmup(ctx, o.VMI, namespace, runID, o.BatchWaitTimeout); err != nil {
		result.Results = append(result.Results, pkgsuites.NewCaseResultErrored("vmi-warmup", time.Now(), time.Now(), err))
		return result
	}

	runStart := time.Now()
	caseResult, _ := s.createVMIsUntilFailure(ctx, o, namespace, runID, global.EtcdNamespace)
	result.Results = append(result.Results, caseResult)
	metricResult, err := s.collectDensityMetrics(ctx, runStart)
	result.Results = append(result.Results, metricResult)
	if err != nil {
		result.Err = err.Error()
	}
	return result
}

func (s *DensitySuite) cleanupRunResult(namespace, runID string, result *pkgsuites.SuiteResult) {
	err := s.cleanup(namespace, runID)
	if err == nil {
		return
	}
	for _, r := range result.Results {
		for _, capacity := range r.CapacityResults {
			capacity.CleanupErr = err.Error()
		}
		r.FinalizeState()
	}
	if result.Err == "" {
		result.Err = err.Error()
	}
}

// warmup creates a single VMI to pre-pull the container image onto each node,
// preventing cold-pull latency from skewing density measurements.
func (s *DensitySuite) warmup(ctx context.Context, spec resource.VMI, namespace, runID string, timeout time.Duration) error {
	spec.Namespace, spec.RunID, spec.Name = namespace, runID, runID+"-density-warmup"
	warmupCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := resource.CreateVMIs(warmupCtx, s.DynClientSet, []resource.VMI{spec}, 0); err != nil {
		return err
	}
	if err := resource.WaitRunning(warmupCtx, s.DynClientSet, namespace, resource.RunLabel+"="+runID, []string{spec.Name}); err != nil {
		return err
	}
	// The warm-up VMI is the only run-labelled VMI at this point.
	return s.cleanup(namespace, runID)
}

func (s *DensitySuite) createVMIsUntilFailure(ctx context.Context, o Options, namespace, runID, etcdNS string) (*pkgsuites.CaseResult, *pkgsuites.CapacityResult) {
	start := time.Now()
	maxReady, err := s.createVMIsInBatches(ctx, o, namespace, runID, etcdNS)
	capResult := &pkgsuites.CapacityResult{Resource: "virtualmachineinstances.kubevirt.io", Max: maxReady}
	if err != nil {
		capResult.Err = err.Error()
	}
	result := pkgsuites.NewCaseResult("vmi-create", start, time.Now(), nil, nil)
	result.CapacityResults = []*pkgsuites.CapacityResult{capResult}
	result.FinalizeState()
	return result, capResult
}

type healthBaseline struct {
	restarts   map[string]int32
	containers map[string]bool
}

// healthPods includes control-plane and Harvester/KubeVirt service pods, not jobs.
func (s *DensitySuite) healthPods(ctx context.Context, etcdNS string) ([]corev1.Pod, error) {
	controlPlane, err := s.K8sClientSet.CoreV1().Pods(etcdNS).List(ctx, metav1.ListOptions{LabelSelector: "tier=control-plane"})
	if err != nil {
		return nil, err
	}
	harvester, err := s.K8sClientSet.CoreV1().Pods("harvester-system").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	pods := controlPlane.Items
	for _, pod := range harvester.Items {
		service := strings.HasPrefix(pod.Name, "harvester-") || strings.HasPrefix(pod.Name, "virt-")
		job := slices.ContainsFunc(pod.OwnerReferences, func(o metav1.OwnerReference) bool { return o.Kind == "Job" })
		if service && !job {
			pods = append(pods, pod)
		}
	}
	if len(pods) == len(controlPlane.Items) {
		return nil, fmt.Errorf("no Harvester/KubeVirt service pods found")
	}
	return pods, nil
}

func (s *DensitySuite) captureBaseline(ctx context.Context, etcdNS string) (healthBaseline, error) {
	pods, err := s.healthPods(ctx, etcdNS)
	if err != nil {
		return healthBaseline{}, fmt.Errorf("capture baseline: %w", err)
	}
	b := healthBaseline{restarts: make(map[string]int32, len(pods)*2), containers: make(map[string]bool)}
	for _, pod := range pods {
		for _, cs := range pod.Status.ContainerStatuses {
			b.restarts[pod.Namespace+"/"+pod.Name+"/"+string(pod.UID)+"/"+cs.Name] = cs.RestartCount
			b.containers[pod.Namespace+"/"+cs.Name] = true
		}
	}
	return b, nil
}

func (s *DensitySuite) checkClusterHealth(ctx context.Context, baseline healthBaseline, etcdNS string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := s.checkNodes(ctx); err != nil {
		return err
	}
	if _, _, err := pkgk8s.EnsureEtcdReady(ctx, s.Clients, etcdNS, 30*time.Second); err != nil {
		return fmt.Errorf("etcd not ready: %w", err)
	}
	return s.checkControlPlanePods(ctx, baseline, etcdNS)
}

func (s *DensitySuite) checkNodes(ctx context.Context) error {
	nodes, err := s.K8sClientSet.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}
	if len(nodes.Items) == 0 {
		return fmt.Errorf("no nodes found")
	}
	for _, node := range nodes.Items {
		ready := false
		for _, cond := range node.Status.Conditions {
			if cond.Type == corev1.NodeReady {
				ready = cond.Status == corev1.ConditionTrue
			}
			if (cond.Type == corev1.NodeMemoryPressure ||
				cond.Type == corev1.NodeDiskPressure ||
				cond.Type == corev1.NodePIDPressure) &&
				cond.Status != corev1.ConditionFalse {
				return fmt.Errorf("node %s: %s condition %s", node.Name, cond.Type, cond.Status)
			}
		}
		if !ready {
			return fmt.Errorf("node %s is not Ready", node.Name)
		}
	}
	return nil
}

func (s *DensitySuite) checkControlPlanePods(ctx context.Context, baseline healthBaseline, etcdNS string) error {
	pods, err := s.healthPods(ctx, etcdNS)
	if err != nil {
		return fmt.Errorf("list health pods: %w", err)
	}
	seen := make(map[string]bool, len(baseline.containers))
	for _, pod := range pods {
		if pod.Status.Phase != corev1.PodRunning || !podutils.IsPodReady(&pod) || pod.DeletionTimestamp != nil {
			return fmt.Errorf("pod %s/%s is not Running and Ready", pod.Namespace, pod.Name)
		}
		for _, cs := range pod.Status.ContainerStatuses {
			seen[pod.Namespace+"/"+cs.Name] = true
			key := pod.Namespace + "/" + pod.Name + "/" + string(pod.UID) + "/" + cs.Name
			if previous, ok := baseline.restarts[key]; ok {
				if delta := cs.RestartCount - previous; delta > 0 {
					return fmt.Errorf("health container %s restarted %d times during run", key, delta)
				}
			} else {
				baseline.restarts[key] = cs.RestartCount
			}
		}
	}
	for service := range baseline.containers {
		if !seen[service] {
			return fmt.Errorf("health service %s disappeared during run", service)
		}
	}
	return nil
}

func (s *DensitySuite) createVMIsInBatches(ctx context.Context, o Options, namespace, runID, etcdNS string) (int, error) {
	baselineCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	baseline, err := s.captureBaseline(baselineCtx, etcdNS)
	cancel()
	if err != nil {
		return 0, err
	}
	if err := s.checkClusterHealth(ctx, baseline, etcdNS); err != nil {
		return 0, err
	}

	var maxReady int
	for ctx.Err() == nil {
		batchSize := o.BatchSize
		if o.MaxVMs > 0 {
			batchSize = min(batchSize, o.MaxVMs-maxReady)
		}
		vmi := o.VMI
		vmi.Namespace, vmi.RunID = namespace, runID
		vmi.Name = fmt.Sprintf("%s-density-vmi", runID)
		vmis := make([]resource.VMI, batchSize)
		for i := range vmis {
			vmis[i] = vmi
			vmis[i].Name = fmt.Sprintf("%s-%d", vmi.Name, maxReady+i)
		}
		vmiNames := make([]string, len(vmis))
		for i, v := range vmis {
			vmiNames[i] = v.Name
		}
		var batchErr error
		if batchErr = resource.CreateVMIs(ctx, s.DynClientSet, vmis, densityRateLimit); batchErr == nil {
			batchCtx, batchCancel := context.WithTimeout(ctx, o.BatchWaitTimeout)
			batchErr = resource.WaitRunning(batchCtx, s.DynClientSet, namespace, resource.RunLabel+"="+runID, vmiNames)
			batchCancel()
		}
		if batchErr != nil {
			return maxReady, batchErr
		}

		// Ensure the vm is up and running and stable.
		ready, err := s.countRunningVMIs(ctx, namespace, runID)
		if err != nil {
			return maxReady, err
		}
		if ready != maxReady+batchSize {
			return maxReady, fmt.Errorf("%d VMIs Running, expected %d", ready, maxReady+batchSize)
		}
		if err := s.checkClusterHealth(ctx, baseline, etcdNS); err != nil {
			return maxReady, err
		}
		maxReady = ready
		if o.MaxVMs > 0 && maxReady >= o.MaxVMs {
			return maxReady, nil
		}
	}
	return maxReady, ctx.Err()
}

func (s *DensitySuite) countRunningVMIs(ctx context.Context, namespace, runID string) (int, error) {
	list, err := s.DynClientSet.Resource(resource.VMIGVR).Namespace(namespace).List(ctx, metav1.ListOptions{LabelSelector: resource.RunLabel + "=" + runID})
	if err != nil {
		return 0, fmt.Errorf("count running VMIs: %w", err)
	}
	var running int
	for _, item := range list.Items {
		if phase, _, _ := unstructured.NestedString(item.Object, "status", "phase"); phase == "Running" {
			running++
		}
	}
	return running, nil
}

func (s *DensitySuite) collectDensityMetrics(ctx context.Context, runStart time.Time) (*pkgsuites.CaseResult, error) {
	start := time.Now()
	metrics, err := s.measure(ctx, dynamicQueries(promWindow(time.Since(runStart)))...)
	if err != nil {
		return pkgsuites.NewCaseResultErrored("density metrics", start, time.Now(), err), err
	}
	return pkgsuites.NewCaseResult("density metrics", start, time.Now(), nil, metrics), nil
}

func (s *DensitySuite) measure(ctx context.Context, queries ...string) ([]*pkgsuites.MetricResult, error) {
	metrics := make([]*pkgsuites.MetricResult, 0, len(queries))
	for _, query := range queries {
		samples, _, err := pkgprom.RunInstant(ctx, s.PromClient, query)
		if err != nil {
			return metrics, err
		}
		metrics = append(metrics, &pkgsuites.MetricResult{Query: query, Samples: samples})
	}
	return metrics, nil
}

func (s *DensitySuite) cleanup(namespace, runID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	selector := metav1.ListOptions{LabelSelector: resource.RunLabel + "=" + runID}
	if err := s.DynClientSet.Resource(resource.VMIGVR).Namespace(namespace).DeleteCollection(ctx, metav1.DeleteOptions{}, selector); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete VMIs: %w", err)
	}

	for {
		list, err := s.DynClientSet.Resource(resource.VMIGVR).Namespace(namespace).List(ctx, selector)
		if err != nil {
			return fmt.Errorf("list VMIs during cleanup: %w", err)
		}
		if len(list.Items) == 0 {
			return nil
		}
		// BackfillObservedNetworkMacAddress after a VMI failure, blocking GC indefinitely.
		// Give the controller 30s to remove its own finalizer before we force it.
		for i := range list.Items {
			obj := &list.Items[i]
			deleted := obj.GetDeletionTimestamp()
			if deleted == nil || len(obj.GetFinalizers()) == 0 || time.Since(deleted.Time) <= 30*time.Second {
				continue
			}
			obj.SetFinalizers(nil)
			_, err := s.DynClientSet.Resource(resource.VMIGVR).Namespace(namespace).Update(ctx, obj, metav1.UpdateOptions{})
			if err != nil && !apierrors.IsNotFound(err) && !apierrors.IsConflict(err) {
				return fmt.Errorf("strip VMI finalizers: %w", err)
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait VMI deletion: %w", ctx.Err())
		case <-time.After(time.Second):
		}
	}
}
