package density

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pkgprom "github.com/harvester/hvperf/pkg/prometheus"
	"github.com/harvester/hvperf/pkg/resource"
	pkgsuites "github.com/harvester/hvperf/pkg/suites"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
)

func TestOptionsValidate(t *testing.T) {
	o := Options{BatchSize: 1, BatchWaitTimeout: time.Minute, VMI: resource.VMI{ContainerDisk: "img", Memory: "64Mi", CPU: "100m"}}
	if err := o.Validate(); err != nil {
		t.Fatal(err)
	}
	o.BatchWaitTimeout = 0
	if err := o.Validate(); err == nil {
		t.Fatal("zero batchTimeout must fail")
	}
}

func TestPromWindowUsesElapsedWithMinimum(t *testing.T) {
	if got := promWindow(time.Second); got != "60s" {
		t.Fatalf("minimum = %s", got)
	}
	if got := promWindow(2 * time.Hour); got != "7200s" {
		t.Fatalf("full run window = %s", got)
	}
}

func TestDynamicQueriesUseVMIRunning(t *testing.T) {
	queries := strings.Join(dynamicQueries("60s"), "\n")
	if !strings.Contains(queries, `phase="Running"`) {
		t.Fatal("missing VMI Running latency query")
	}
	if strings.Contains(queries, "virtualmachine") {
		t.Fatal("must not query VM readiness")
	}
	for _, grouping := range []string{"sum by (namespace)", "sum by (node)"} {
		if strings.Count(queries, grouping) != 2 {
			t.Fatalf("need CPU and memory queries for %s", grouping)
		}
	}
	if strings.Contains(queries, "namespace, node") {
		t.Fatal("namespace metrics must not be grouped by node")
	}
}

func TestCountRunningVMIs(t *testing.T) {
	objs := []runtime.Object{vmiWithPhase("run-vmi-0", "Running"), vmiWithPhase("run-vmi-1", "Pending"), vmiWithPhase("run-vmi-2", "Running")}
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{resource.VMIGVR: "VirtualMachineInstanceList"}, objs...)
	s := &DensitySuite{Clients: &pkgsuites.Clients{DynClientSet: dyn}}
	running, err := s.countRunningVMIs(context.Background(), "ns", "run")
	if err != nil {
		t.Fatal(err)
	}
	if running != 2 {
		t.Fatalf("running = %d, want 2", running)
	}
}

func TestCreateVMIsInBatchesStopsOnFailure(t *testing.T) {
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{resource.VMIGVR: "VirtualMachineInstanceList", corev1.SchemeGroupVersion.WithResource("pods"): "PodList"})
	s := &DensitySuite{Clients: &pkgsuites.Clients{DynClientSet: dyn, K8sClientSet: healthyClient()}}
	o := Options{BatchSize: 2, BatchWaitTimeout: 100 * time.Millisecond, VMI: resource.VMI{ContainerDisk: "img", Memory: "64Mi", CPU: "100m"}}
	max, err := s.createVMIsInBatches(context.Background(), o, "ns", "run", "kube-system")
	if err == nil || max != 0 {
		t.Fatalf("ramp must stop on timeout: max=%d, err=%v", max, err)
	}
}

func TestCapacityIncludesSchedulingError(t *testing.T) {
	pod := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": "launcher", "namespace": "ns", "labels": map[string]any{"kubevirt.io/created-by": "vmi-uid", "kubevirt.io": "virt-launcher"}},
		"status":   map[string]any{"conditions": []any{map[string]any{"type": "PodScheduled", "status": "False", "reason": "Unschedulable", "message": "Insufficient memory"}}},
	}}
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{resource.VMIGVR: "VirtualMachineInstanceList", corev1.SchemeGroupVersion.WithResource("pods"): "PodList"}, pod)
	dyn.PrependReactor("create", "virtualmachineinstances", func(action k8stesting.Action) (bool, runtime.Object, error) {
		action.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).SetUID("vmi-uid")
		return false, nil, nil
	})
	s := &DensitySuite{Clients: &pkgsuites.Clients{DynClientSet: dyn, K8sClientSet: healthyClient()}}
	o := Options{BatchSize: 2, BatchWaitTimeout: 100 * time.Millisecond, VMI: resource.VMI{ContainerDisk: "img", Memory: "64Mi", CPU: "100m"}}
	result, capacity := s.createVMIsUntilFailure(context.Background(), o, "ns", "run", "kube-system")
	if !strings.Contains(capacity.Err, "Insufficient memory") || capacity.Max != 0 || result.State != pkgsuites.CaseResultStatePassed {
		t.Fatalf("capacity = %+v, state = %s", capacity, result.State)
	}
}

func TestBatchCapacityOnlyAdvancesAfterFullHealthyBatch(t *testing.T) {
	for _, failure := range []string{"none", "create", "pressure", "lost-running", "preflight"} {
		t.Run(failure, func(t *testing.T) {
			k8s := healthyClient()
			if failure == "preflight" {
				node, _ := k8s.CoreV1().Nodes().Get(context.Background(), "node", metav1.GetOptions{})
				node.Status.Conditions[0].Status = corev1.ConditionFalse
				if _, err := k8s.CoreV1().Nodes().UpdateStatus(context.Background(), node, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{resource.VMIGVR: "VirtualMachineInstanceList", corev1.SchemeGroupVersion.WithResource("pods"): "PodList"})
			dyn.PrependReactor("create", "virtualmachineinstances", func(action k8stesting.Action) (bool, runtime.Object, error) {
				obj := action.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured)
				if obj.GetName() == "run-density-vmi-5" {
					if failure == "create" {
						return true, nil, fmt.Errorf("create failed")
					}
					if failure == "pressure" {
						node, _ := k8s.CoreV1().Nodes().Get(context.Background(), "node", metav1.GetOptions{})
						node.Status.Conditions = append(node.Status.Conditions, corev1.NodeCondition{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionTrue})
						if _, err := k8s.CoreV1().Nodes().UpdateStatus(context.Background(), node, metav1.UpdateOptions{}); err != nil {
							return true, nil, err
						}
					}
					if failure == "lost-running" {
						old, err := dyn.Tracker().Get(resource.VMIGVR, "ns", "run-density-vmi-0")
						if err != nil {
							return true, nil, err
						}
						u := old.(*unstructured.Unstructured)
						_ = unstructured.SetNestedField(u.Object, "Failed", "status", "phase")
						if err := dyn.Tracker().Update(resource.VMIGVR, u, "ns"); err != nil {
							return true, nil, err
						}
					}
				}
				_ = unstructured.SetNestedField(obj.Object, "Running", "status", "phase")
				return false, nil, nil
			})
			s := &DensitySuite{Clients: &pkgsuites.Clients{DynClientSet: dyn, K8sClientSet: k8s}}
			o := Options{BatchSize: 2, MaxVMs: 7, BatchWaitTimeout: time.Second, VMI: resource.VMI{ContainerDisk: "img", Memory: "64Mi", CPU: "100m"}}
			result, capacity := s.createVMIsUntilFailure(context.Background(), o, "ns", "run", "kube-system")
			want := 4
			if failure == "none" {
				want = 7
			}
			if failure == "preflight" {
				want = 0
				if len(dyn.Actions()) != 0 {
					t.Fatal("unhealthy cluster must not create VMIs")
				}
			}
			if capacity.Max != want || (capacity.Err != "") != (failure != "none") {
				t.Fatalf("capacity = %+v, want Max=%d", capacity, want)
			}
			if result.CapacityResults[0] != capacity || result.State != pkgsuites.CaseResultStatePassed {
				t.Fatalf("capacity result = %+v", result)
			}
			capacity.CleanupErr = "cleanup failed"
			result.FinalizeState()
			if result.State != pkgsuites.CaseResultStateErrored {
				t.Fatal("cleanup error must fail case")
			}
		})
	}
}

func TestHealthGate(t *testing.T) {
	for _, failure := range []string{"none", "not-ready", "pressure", "restart", "pending", "completed", "replaced", "added", "handler-restart", "operator-pending", "deleted", "empty-services", "renamed"} {
		t.Run(failure, func(t *testing.T) {
			client := healthyClient()
			s := &DensitySuite{Clients: &pkgsuites.Clients{K8sClientSet: client}}
			ctx := context.Background()
			podName := "virt-api-0"
			if failure == "handler-restart" {
				podName = "virt-handler-0"
			}
			if failure == "operator-pending" {
				podName = "virt-operator-0"
			}
			if failure == "replaced" {
				old, _ := client.CoreV1().Pods("harvester-system").Get(ctx, podName, metav1.GetOptions{})
				old.Status.ContainerStatuses[0].RestartCount = 5
				if _, err := client.CoreV1().Pods(old.Namespace).UpdateStatus(ctx, old, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			baseline, err := s.captureBaseline(ctx, "kube-system")
			if err != nil {
				t.Fatal(err)
			}
			node, _ := client.CoreV1().Nodes().Get(ctx, "node", metav1.GetOptions{})
			pod, _ := client.CoreV1().Pods("harvester-system").Get(ctx, podName, metav1.GetOptions{})
			switch failure {
			case "not-ready":
				node.Status.Conditions[0].Status = corev1.ConditionFalse
			case "pressure":
				node.Status.Conditions = append(node.Status.Conditions, corev1.NodeCondition{Type: corev1.NodeDiskPressure, Status: corev1.ConditionTrue})
			case "restart", "handler-restart":
				pod.Status.ContainerStatuses[0].RestartCount++
			case "pending", "operator-pending":
				pod.Status.Phase = corev1.PodPending
			case "completed":
				pod.Status.Phase = corev1.PodSucceeded
			case "replaced":
				pod.UID = "replacement"
				pod.Status.ContainerStatuses[0].RestartCount = 0
			case "added", "renamed":
				added := pod.DeepCopy()
				added.Name = "virt-api-1"
				if _, err := client.CoreV1().Pods(added.Namespace).Create(ctx, added, metav1.CreateOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := client.CoreV1().Nodes().UpdateStatus(ctx, node, metav1.UpdateOptions{}); err != nil {
				t.Fatal(err)
			}
			if _, err := client.CoreV1().Pods(pod.Namespace).UpdateStatus(ctx, pod, metav1.UpdateOptions{}); err != nil {
				t.Fatal(err)
			}
			if failure == "deleted" || failure == "renamed" {
				if err := client.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			if failure == "empty-services" {
				for _, name := range []string{"virt-api-0", "virt-handler-0", "virt-operator-0"} {
					if err := client.CoreV1().Pods(pod.Namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
						t.Fatal(err)
					}
				}
			}
			wantError := failure != "none" && failure != "replaced" && failure != "added" && failure != "renamed"
			if err := s.checkClusterHealth(ctx, baseline, "kube-system"); (err != nil) != wantError {
				t.Fatalf("health error = %v", err)
			}
			if failure == "added" || failure == "replaced" {
				name := pod.Name
				if failure == "added" {
					name = "virt-api-1"
				}
				added, err := client.CoreV1().Pods(pod.Namespace).Get(ctx, name, metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				added.Status.ContainerStatuses[0].RestartCount++
				if _, err := client.CoreV1().Pods(added.Namespace).UpdateStatus(ctx, added, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
				if err := s.checkClusterHealth(ctx, baseline, "kube-system"); err == nil {
					t.Fatal("new or replaced pod restart must fail subsequent gate")
				}
			}
		})
	}
}

func TestCollectDensityMetricsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	api, err := pkgprom.New(server.URL, &rest.Config{})
	if err != nil {
		t.Fatal(err)
	}
	s := &DensitySuite{Clients: &pkgsuites.Clients{PromClient: api}}
	result, err := s.collectDensityMetrics(context.Background(), time.Now())
	if err == nil || result.Err != err.Error() || result.State != pkgsuites.CaseResultStateErrored {
		t.Fatalf("metrics result = %+v, err = %v", result, err)
	}
}

func healthyClient() *k8sfake.Clientset {
	pod := func(namespace, name string, labels map[string]string) *corev1.Pod {
		return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, UID: "original", Labels: labels}, Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}, ContainerStatuses: []corev1.ContainerStatus{{Name: strings.TrimSuffix(name, "-0"), Ready: true}}}}
	}
	return k8sfake.NewClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node"}, Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}},
		pod("kube-system", "etcd-0", map[string]string{"tier": "control-plane", "component": "etcd"}),
		pod("harvester-system", "virt-api-0", nil),
		pod("harvester-system", "virt-handler-0", nil),
		pod("harvester-system", "virt-operator-0", nil),
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "harvester-system", Name: "harvester-job-0", OwnerReferences: []metav1.OwnerReference{{Kind: "Job"}}}, Status: corev1.PodStatus{Phase: corev1.PodPending}},
	)
}

func vmiWithPhase(name, phase string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "kubevirt.io/v1", "kind": "VirtualMachineInstance", "metadata": map[string]any{"name": name, "namespace": "ns", "labels": map[string]any{resource.RunLabel: "run"}}, "status": map[string]any{"phase": phase}}}
}
