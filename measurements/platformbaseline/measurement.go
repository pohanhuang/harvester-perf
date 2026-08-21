package platformbaseline

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	"k8s.io/perf-tests/clusterloader2/pkg/measurement"
)

const platformBaselineMeasurementName = "PlatformBaseline"

func init() {
	if err := measurement.Register(platformBaselineMeasurementName, createPlatformBaselineMeasurement); err != nil {
		panic(fmt.Sprintf("Cannot register %s: %v", platformBaselineMeasurementName, err))
	}
}

func createPlatformBaselineMeasurement() measurement.Measurement {
	return &platformBaselineMeasurement{}
}

type platformBaselineMeasurement struct {
	startTime time.Time
}

type platformBaselineReport struct {
	WindowStart        time.Time
	WindowEnd          time.Time
	CollectedAt        time.Time
	SampleCount        int
	SampleInterval     string
	SampleDuration     string
	NodeMetrics        []nodeMetricSummary
	NodeAllocations    []nodeAllocationSummary
	NamespaceSummaries []namespaceSummary
	GroupSummaries     []namespaceSummary
	GrandTotal         resourceTotals
	PlatformGrandTotal resourceTotals
}

type nodeMetricSummary struct {
	Name        string
	CPUMilli    int64
	MemoryBytes int64
}

type nodeAllocationSummary struct {
	Name                string
	CPURequestsMilli    int64
	CPULimitsMilli      int64
	MemoryRequestsBytes int64
	MemoryLimitsBytes   int64
}

type namespaceSummary struct {
	Name        string
	Pods        int
	CPUMilli    int64
	MemoryBytes int64
	TopPods     []podResourceUsage
}

type podResourceUsage struct {
	Namespace   string
	Name        string
	NodeName    string
	CPUMilli    int64
	MemoryBytes int64
}

type resourceTotals struct {
	Pods        int
	CPUMilli    int64
	MemoryBytes int64
}

type platformBaselineSummary struct {
	report    *platformBaselineReport
	startTime time.Time
	format    string
}

func (m *platformBaselineMeasurement) Execute(config *measurement.Config) ([]measurement.Summary, error) {
	action, ok := config.Params["action"].(string)
	if !ok {
		return nil, fmt.Errorf("action parameter is required")
	}

	switch action {
	case "start":
		m.startTime = time.Now()
		return nil, nil
	case "gather":
		measurementConfig := configFromParams(config.Params)
		report, err := collectPlatformBaselineAverage(config.ClusterFramework.GetClientSets().GetClient(), measurementConfig.Duration, measurementConfig.Interval)
		if err != nil {
			return nil, err
		}
		startTime := m.startTime
		if startTime.IsZero() {
			startTime = report.CollectedAt
		}
		return []measurement.Summary{
			&platformBaselineSummary{
				report:    report,
				startTime: startTime,
				format:    "txt",
			},
			&platformBaselineSummary{
				report:    report,
				startTime: startTime,
				format:    "json",
			},
		}, nil
	default:
		return nil, fmt.Errorf("unknown action: %s", action)
	}
}

func (m *platformBaselineMeasurement) Dispose() {}

func (m *platformBaselineMeasurement) String() string {
	return platformBaselineMeasurementName
}

func (s *platformBaselineSummary) SummaryName() string {
	return platformBaselineMeasurementName
}

func (s *platformBaselineSummary) SummaryExt() string {
	if s.format == "json" {
		return "json"
	}
	return "txt"
}

func (s *platformBaselineSummary) SummaryTime() time.Time {
	return s.startTime
}

func (s *platformBaselineSummary) SummaryContent() string {
	if s.format == "json" {
		data, err := json.MarshalIndent(s.report, "", "  ")
		if err != nil {
			return fmt.Sprintf("{\"error\": %q}", err.Error())
		}
		return string(data)
	}

	var b strings.Builder

	fmt.Fprintf(&b, "Platform Baseline Summary\n")
	fmt.Fprintf(&b, "=========================\n")
	fmt.Fprintf(&b, "Collected at: %s\n", s.report.CollectedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "Window: %s -> %s\n", s.report.WindowStart.Format(time.RFC3339), s.report.WindowEnd.Format(time.RFC3339))
	fmt.Fprintf(&b, "Samples: %d (interval %s, duration %s)\n\n", s.report.SampleCount, s.report.SampleInterval, s.report.SampleDuration)

	fmt.Fprintf(&b, "Node Metrics (metrics.k8s.io)\n")
	for _, node := range s.report.NodeMetrics {
		fmt.Fprintf(&b, "- %s: CPU %dm, Memory %s\n", node.Name, node.CPUMilli, formatMi(node.MemoryBytes))
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "Scheduler Allocation (requests/limits from pod specs)\n")
	for _, node := range s.report.NodeAllocations {
		fmt.Fprintf(
			&b,
			"- %s: requests CPU %dm, Memory %s | limits CPU %dm, Memory %s\n",
			node.Name,
			node.CPURequestsMilli,
			formatMi(node.MemoryRequestsBytes),
			node.CPULimitsMilli,
			formatMi(node.MemoryLimitsBytes),
		)
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "Namespace Breakdown\n")
	for _, ns := range s.report.NamespaceSummaries {
		fmt.Fprintf(&b, "- %s: %d pods, CPU %dm, Memory %s\n", ns.Name, ns.Pods, ns.CPUMilli, formatMi(ns.MemoryBytes))
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "Platform Groups\n")
	for _, group := range s.report.GroupSummaries {
		fmt.Fprintf(&b, "- %s: %d pods, CPU %dm, Memory %s\n", group.Name, group.Pods, group.CPUMilli, formatMi(group.MemoryBytes))
		for _, pod := range group.TopPods {
			fmt.Fprintf(&b, "  %s/%s: CPU %dm, Memory %s\n", pod.Namespace, pod.Name, pod.CPUMilli, formatMi(pod.MemoryBytes))
		}
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "Grand Total: %d pods, CPU %dm, Memory %s\n", s.report.GrandTotal.Pods, s.report.GrandTotal.CPUMilli, formatMi(s.report.GrandTotal.MemoryBytes))
	fmt.Fprintf(&b, "Platform Total: %d pods, CPU %dm, Memory %s\n", s.report.PlatformGrandTotal.Pods, s.report.PlatformGrandTotal.CPUMilli, formatMi(s.report.PlatformGrandTotal.MemoryBytes))

	return b.String()
}

func collectPlatformBaseline(client kubernetes.Interface) (*platformBaselineReport, error) {
	podMetrics, err := listPodMetrics(client)
	if err != nil {
		return nil, fmt.Errorf("listing pod metrics: %w", err)
	}

	nodeMetrics, err := listNodeMetrics(client)
	if err != nil {
		return nil, fmt.Errorf("listing node metrics: %w", err)
	}

	pods, err := client.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{ResourceVersion: "0"})
	if err != nil {
		return nil, fmt.Errorf("listing pods: %w", err)
	}

	podIndex := make(map[string]corev1.Pod, len(pods.Items))
	namespaceTotals := map[string]*namespaceSummary{}
	groupTotals := map[string]*namespaceSummary{}
	grandTotal := resourceTotals{}
	platformTotal := resourceTotals{}

	for _, pod := range pods.Items {
		podIndex[pod.Namespace+"/"+pod.Name] = pod
	}

	for _, item := range podMetrics.Items {
		key := item.Namespace + "/" + item.Name
		pod, ok := podIndex[key]
		if !ok {
			klog.V(2).Infof("Skipping metrics for pod %s because spec was not found", key)
			continue
		}

		usage := summarizePodUsage(item, pod.Spec.NodeName)
		addToNamespaceSummary(namespaceTotals, item.Namespace, usage)
		addToNamespaceSummary(groupTotals, classifyPlatformGroup(pod), usage)

		grandTotal.Pods++
		grandTotal.CPUMilli += usage.CPUMilli
		grandTotal.MemoryBytes += usage.MemoryBytes

		if isPlatformNamespace(pod.Namespace) {
			platformTotal.Pods++
			platformTotal.CPUMilli += usage.CPUMilli
			platformTotal.MemoryBytes += usage.MemoryBytes
		}
	}

	report := &platformBaselineReport{
		CollectedAt:        time.Now().UTC(),
		NodeMetrics:        summarizeNodeMetrics(nodeMetrics),
		NodeAllocations:    summarizeNodeAllocations(pods.Items),
		NamespaceSummaries: flattenSummaries(namespaceTotals),
		GroupSummaries:     flattenSummaries(groupTotals),
		GrandTotal:         grandTotal,
		PlatformGrandTotal: platformTotal,
	}

	return report, nil
}

func collectPlatformBaselineAverage(client kubernetes.Interface, duration, interval time.Duration) (*platformBaselineReport, error) {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	if duration <= 0 {
		duration = interval
	}

	start := time.Now().UTC()
	deadline := start.Add(duration)

	snapshots := make([]*platformBaselineReport, 0, 1)
	for {
		report, err := collectPlatformBaseline(client)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, report)

		nextSampleAt := start.Add(time.Duration(len(snapshots)) * interval)
		if nextSampleAt.After(deadline) {
			break
		}
		time.Sleep(time.Until(nextSampleAt))
	}

	return averagePlatformBaselineReports(snapshots, start, time.Now().UTC(), interval, duration), nil
}

func listPodMetrics(client kubernetes.Interface) (*metricsv1beta1.PodMetricsList, error) {
	data, err := client.CoreV1().RESTClient().Get().
		SetHeader("Accept", "application/json").
		AbsPath("/apis/metrics.k8s.io/v1beta1/pods").
		DoRaw(context.TODO())
	if err != nil {
		return nil, err
	}

	out := &metricsv1beta1.PodMetricsList{}
	if err := json.Unmarshal(data, out); err != nil {
		return nil, fmt.Errorf("decoding pod metrics JSON: %w (response prefix: %q)", err, responsePreview(data))
	}
	return out, nil
}

func listNodeMetrics(client kubernetes.Interface) (*metricsv1beta1.NodeMetricsList, error) {
	data, err := client.CoreV1().RESTClient().Get().
		SetHeader("Accept", "application/json").
		AbsPath("/apis/metrics.k8s.io/v1beta1/nodes").
		DoRaw(context.TODO())
	if err != nil {
		return nil, err
	}

	out := &metricsv1beta1.NodeMetricsList{}
	if err := json.Unmarshal(data, out); err != nil {
		return nil, fmt.Errorf("decoding node metrics JSON: %w (response prefix: %q)", err, responsePreview(data))
	}
	return out, nil
}

func responsePreview(data []byte) string {
	const limit = 80
	if len(data) <= limit {
		return string(data)
	}
	return string(data[:limit])
}

func averagePlatformBaselineReports(reports []*platformBaselineReport, windowStart, windowEnd time.Time, interval, duration time.Duration) *platformBaselineReport {
	nodeMetrics := map[string]*nodeMetricAccumulator{}
	nodeAllocations := map[string]*nodeAllocationAccumulator{}
	namespaceSummaries := map[string]*namespaceAccumulator{}
	groupSummaries := map[string]*namespaceAccumulator{}
	grandTotal := resourceTotalsAccumulator{}
	platformTotal := resourceTotalsAccumulator{}

	for _, report := range reports {
		for _, item := range report.NodeMetrics {
			acc := ensureNodeMetricAccumulator(nodeMetrics, item.Name)
			acc.CPUMilli += item.CPUMilli
			acc.MemoryBytes += item.MemoryBytes
			acc.Samples++
		}
		for _, item := range report.NodeAllocations {
			acc := ensureNodeAllocationAccumulator(nodeAllocations, item.Name)
			acc.CPURequestsMilli += item.CPURequestsMilli
			acc.CPULimitsMilli += item.CPULimitsMilli
			acc.MemoryRequestsBytes += item.MemoryRequestsBytes
			acc.MemoryLimitsBytes += item.MemoryLimitsBytes
			acc.Samples++
		}
		for _, item := range report.NamespaceSummaries {
			acc := ensureNamespaceAccumulator(namespaceSummaries, item.Name)
			acc.Pods += int64(item.Pods)
			acc.CPUMilli += item.CPUMilli
			acc.MemoryBytes += item.MemoryBytes
			acc.Samples++
			for _, pod := range item.TopPods {
				podAcc := ensurePodAccumulator(acc.TopPods, pod.Namespace, pod.Name, pod.NodeName)
				podAcc.CPUMilli += pod.CPUMilli
				podAcc.MemoryBytes += pod.MemoryBytes
				podAcc.Samples++
			}
		}
		for _, item := range report.GroupSummaries {
			acc := ensureNamespaceAccumulator(groupSummaries, item.Name)
			acc.Pods += int64(item.Pods)
			acc.CPUMilli += item.CPUMilli
			acc.MemoryBytes += item.MemoryBytes
			acc.Samples++
			for _, pod := range item.TopPods {
				podAcc := ensurePodAccumulator(acc.TopPods, pod.Namespace, pod.Name, pod.NodeName)
				podAcc.CPUMilli += pod.CPUMilli
				podAcc.MemoryBytes += pod.MemoryBytes
				podAcc.Samples++
			}
		}

		grandTotal.Pods += int64(report.GrandTotal.Pods)
		grandTotal.CPUMilli += report.GrandTotal.CPUMilli
		grandTotal.MemoryBytes += report.GrandTotal.MemoryBytes
		grandTotal.Samples++

		platformTotal.Pods += int64(report.PlatformGrandTotal.Pods)
		platformTotal.CPUMilli += report.PlatformGrandTotal.CPUMilli
		platformTotal.MemoryBytes += report.PlatformGrandTotal.MemoryBytes
		platformTotal.Samples++
	}

	return &platformBaselineReport{
		WindowStart:        windowStart,
		WindowEnd:          windowEnd,
		CollectedAt:        windowEnd,
		SampleCount:        len(reports),
		SampleInterval:     interval.String(),
		SampleDuration:     duration.String(),
		NodeMetrics:        flattenNodeMetricAccumulators(nodeMetrics),
		NodeAllocations:    flattenNodeAllocationAccumulators(nodeAllocations),
		NamespaceSummaries: flattenNamespaceAccumulators(namespaceSummaries),
		GroupSummaries:     flattenNamespaceAccumulators(groupSummaries),
		GrandTotal:         grandTotal.average(),
		PlatformGrandTotal: platformTotal.average(),
	}
}

type nodeMetricAccumulator struct {
	Name        string
	CPUMilli    int64
	MemoryBytes int64
	Samples     int
}

type nodeAllocationAccumulator struct {
	Name                string
	CPURequestsMilli    int64
	CPULimitsMilli      int64
	MemoryRequestsBytes int64
	MemoryLimitsBytes   int64
	Samples             int
}

type namespaceAccumulator struct {
	Name        string
	Pods        int64
	CPUMilli    int64
	MemoryBytes int64
	Samples     int
	TopPods     map[string]*podAccumulator
}

type podAccumulator struct {
	Namespace   string
	Name        string
	NodeName    string
	CPUMilli    int64
	MemoryBytes int64
	Samples     int
}

type resourceTotalsAccumulator struct {
	Pods        int64
	CPUMilli    int64
	MemoryBytes int64
	Samples     int
}

func (a resourceTotalsAccumulator) average() resourceTotals {
	if a.Samples == 0 {
		return resourceTotals{}
	}
	return resourceTotals{
		Pods:        int(divRound(a.Pods, int64(a.Samples))),
		CPUMilli:    divRound(a.CPUMilli, int64(a.Samples)),
		MemoryBytes: divRound(a.MemoryBytes, int64(a.Samples)),
	}
}

func ensureNodeMetricAccumulator(items map[string]*nodeMetricAccumulator, name string) *nodeMetricAccumulator {
	acc, ok := items[name]
	if !ok {
		acc = &nodeMetricAccumulator{Name: name}
		items[name] = acc
	}
	return acc
}

func ensureNodeAllocationAccumulator(items map[string]*nodeAllocationAccumulator, name string) *nodeAllocationAccumulator {
	acc, ok := items[name]
	if !ok {
		acc = &nodeAllocationAccumulator{Name: name}
		items[name] = acc
	}
	return acc
}

func ensureNamespaceAccumulator(items map[string]*namespaceAccumulator, name string) *namespaceAccumulator {
	acc, ok := items[name]
	if !ok {
		acc = &namespaceAccumulator{Name: name, TopPods: map[string]*podAccumulator{}}
		items[name] = acc
	}
	return acc
}

func ensurePodAccumulator(items map[string]*podAccumulator, namespace, name, nodeName string) *podAccumulator {
	key := namespace + "/" + name
	acc, ok := items[key]
	if !ok {
		acc = &podAccumulator{
			Namespace: namespace,
			Name:      name,
			NodeName:  nodeName,
		}
		items[key] = acc
	}
	return acc
}

func flattenNodeMetricAccumulators(items map[string]*nodeMetricAccumulator) []nodeMetricSummary {
	out := make([]nodeMetricSummary, 0, len(items))
	for _, item := range items {
		if item.Samples == 0 {
			continue
		}
		out = append(out, nodeMetricSummary{
			Name:        item.Name,
			CPUMilli:    divRound(item.CPUMilli, int64(item.Samples)),
			MemoryBytes: divRound(item.MemoryBytes, int64(item.Samples)),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func flattenNodeAllocationAccumulators(items map[string]*nodeAllocationAccumulator) []nodeAllocationSummary {
	out := make([]nodeAllocationSummary, 0, len(items))
	for _, item := range items {
		if item.Samples == 0 {
			continue
		}
		out = append(out, nodeAllocationSummary{
			Name:                item.Name,
			CPURequestsMilli:    divRound(item.CPURequestsMilli, int64(item.Samples)),
			CPULimitsMilli:      divRound(item.CPULimitsMilli, int64(item.Samples)),
			MemoryRequestsBytes: divRound(item.MemoryRequestsBytes, int64(item.Samples)),
			MemoryLimitsBytes:   divRound(item.MemoryLimitsBytes, int64(item.Samples)),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func flattenNamespaceAccumulators(items map[string]*namespaceAccumulator) []namespaceSummary {
	out := make([]namespaceSummary, 0, len(items))
	for _, item := range items {
		if item.Samples == 0 {
			continue
		}
		out = append(out, namespaceSummary{
			Name:        item.Name,
			Pods:        int(divRound(item.Pods, int64(item.Samples))),
			CPUMilli:    divRound(item.CPUMilli, int64(item.Samples)),
			MemoryBytes: divRound(item.MemoryBytes, int64(item.Samples)),
			TopPods:     flattenPodAccumulators(item.TopPods),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func flattenPodAccumulators(items map[string]*podAccumulator) []podResourceUsage {
	out := make([]podResourceUsage, 0, len(items))
	for _, item := range items {
		if item.Samples == 0 {
			continue
		}
		out = append(out, podResourceUsage{
			Namespace:   item.Namespace,
			Name:        item.Name,
			NodeName:    item.NodeName,
			CPUMilli:    divRound(item.CPUMilli, int64(item.Samples)),
			MemoryBytes: divRound(item.MemoryBytes, int64(item.Samples)),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].MemoryBytes == out[j].MemoryBytes {
			return out[i].Name < out[j].Name
		}
		return out[i].MemoryBytes > out[j].MemoryBytes
	})
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

func divRound(total, divisor int64) int64 {
	if divisor == 0 {
		return 0
	}
	return (total + divisor/2) / divisor
}

func summarizePodUsage(item metricsv1beta1.PodMetrics, nodeName string) podResourceUsage {
	var cpuMilli int64
	var memoryBytes int64

	for _, container := range item.Containers {
		cpuMilli += quantityMilli(container.Usage[corev1.ResourceCPU])
		memoryBytes += quantityValue(container.Usage[corev1.ResourceMemory])
	}

	return podResourceUsage{
		Namespace:   item.Namespace,
		Name:        item.Name,
		NodeName:    nodeName,
		CPUMilli:    cpuMilli,
		MemoryBytes: memoryBytes,
	}
}

func summarizeNodeMetrics(metrics *metricsv1beta1.NodeMetricsList) []nodeMetricSummary {
	out := make([]nodeMetricSummary, 0, len(metrics.Items))
	for _, item := range metrics.Items {
		out = append(out, nodeMetricSummary{
			Name:        item.Name,
			CPUMilli:    quantityMilli(item.Usage[corev1.ResourceCPU]),
			MemoryBytes: quantityValue(item.Usage[corev1.ResourceMemory]),
		})
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func summarizeNodeAllocations(pods []corev1.Pod) []nodeAllocationSummary {
	allocations := map[string]*nodeAllocationSummary{}

	for _, pod := range pods {
		if pod.Spec.NodeName == "" || isTerminalPod(pod.Status.Phase) {
			continue
		}

		summary := allocations[pod.Spec.NodeName]
		if summary == nil {
			summary = &nodeAllocationSummary{Name: pod.Spec.NodeName}
			allocations[pod.Spec.NodeName] = summary
		}

		for _, container := range pod.Spec.InitContainers {
			addContainerResources(summary, container.Resources)
		}
		for _, container := range pod.Spec.Containers {
			addContainerResources(summary, container.Resources)
		}
	}

	out := make([]nodeAllocationSummary, 0, len(allocations))
	for _, summary := range allocations {
		out = append(out, *summary)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func addContainerResources(summary *nodeAllocationSummary, resources corev1.ResourceRequirements) {
	summary.CPURequestsMilli += quantityMilli(resources.Requests[corev1.ResourceCPU])
	summary.CPULimitsMilli += quantityMilli(resources.Limits[corev1.ResourceCPU])
	summary.MemoryRequestsBytes += quantityValue(resources.Requests[corev1.ResourceMemory])
	summary.MemoryLimitsBytes += quantityValue(resources.Limits[corev1.ResourceMemory])
}

func addToNamespaceSummary(summaries map[string]*namespaceSummary, key string, usage podResourceUsage) {
	summary := summaries[key]
	if summary == nil {
		summary = &namespaceSummary{Name: key}
		summaries[key] = summary
	}

	summary.Pods++
	summary.CPUMilli += usage.CPUMilli
	summary.MemoryBytes += usage.MemoryBytes
	summary.TopPods = append(summary.TopPods, usage)
}

func flattenSummaries(summaries map[string]*namespaceSummary) []namespaceSummary {
	out := make([]namespaceSummary, 0, len(summaries))
	for _, summary := range summaries {
		sort.Slice(summary.TopPods, func(i, j int) bool {
			if summary.TopPods[i].MemoryBytes == summary.TopPods[j].MemoryBytes {
				return summary.TopPods[i].Name < summary.TopPods[j].Name
			}
			return summary.TopPods[i].MemoryBytes > summary.TopPods[j].MemoryBytes
		})
		if len(summary.TopPods) > 5 {
			summary.TopPods = summary.TopPods[:5]
		}
		out = append(out, *summary)
	}

	sort.Slice(out, func(i, j int) bool {
		return summaryOrder(out[i].Name) < summaryOrder(out[j].Name)
	})

	return out
}

func summaryOrder(name string) string {
	order := map[string]string{
		"kube-system":               "01",
		"harvester-system":          "02",
		"harvester-system/kubevirt": "03",
		"harvester-system/cdi":      "04",
		"harvester-system/core":     "05",
		"longhorn-system":           "06",
		"management":                "07",
		"other":                     "99",
	}

	if prefix, ok := order[name]; ok {
		return prefix + "-" + name
	}
	return "50-" + name
}

func classifyPlatformGroup(pod corev1.Pod) string {
	switch pod.Namespace {
	case "kube-system":
		return "kube-system"
	case "harvester-system":
		if strings.HasPrefix(pod.Name, "virt-") {
			return "harvester-system/kubevirt"
		}
		if strings.HasPrefix(pod.Name, "cdi-") {
			return "harvester-system/cdi"
		}
		return "harvester-system/core"
	case "longhorn-system":
		return "longhorn-system"
	default:
		if isManagementNamespace(pod.Namespace) {
			return "management"
		}
		return "other"
	}
}

func isPlatformNamespace(namespace string) bool {
	return namespace == "kube-system" ||
		namespace == "harvester-system" ||
		namespace == "longhorn-system" ||
		isManagementNamespace(namespace)
}

func isManagementNamespace(namespace string) bool {
	return strings.HasPrefix(namespace, "cattle-")
}

func isTerminalPod(phase corev1.PodPhase) bool {
	return phase == corev1.PodSucceeded || phase == corev1.PodFailed
}

func quantityMilli(q resource.Quantity) int64 {
	return q.MilliValue()
}

func quantityValue(q resource.Quantity) int64 {
	return q.Value()
}

func formatMi(bytes int64) string {
	return fmt.Sprintf("%d Mi", bytes/(1024*1024))
}
