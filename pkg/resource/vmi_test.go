package resource

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8swatch "k8s.io/apimachinery/pkg/watch"
	dynfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestVMIObjectUsesContainerDiskAndRunLabel(t *testing.T) {
	u, err := vmiObject(VMI{Namespace: "ns", Name: "run-vmi-0", RunID: "run", ContainerDisk: "registry.example/cirros:1", Memory: "128Mi", CPU: "200m"})
	if err != nil {
		t.Fatal(err)
	}
	if got := u.GetLabels()[RunLabel]; got != "run" {
		t.Fatalf("run label = %q, want run", got)
	}
	if _, ok := u.GetAnnotations()["harvesterhci.io/volumeClaimTemplates"]; ok {
		t.Fatal("containerdisk VMI must not create a PVC")
	}
	volumes, _, _ := unstructured.NestedSlice(u.Object, "spec", "volumes")
	image := volumes[0].(map[string]any)["containerDisk"].(map[string]any)["image"]
	if image != "registry.example/cirros:1" {
		t.Fatalf("containerDisk image = %q", image)
	}
}

func TestCreateAndWaitVMIReturnsContextDeadline(t *testing.T) {
	for _, scheduled := range []string{"False", "True", "missing", "forbidden"} {
		t.Run(scheduled, func(t *testing.T) {
			pod := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1", "kind": "Pod",
				"metadata": map[string]any{"name": "launcher", "namespace": "ns", "labels": map[string]any{"kubevirt.io/created-by": "vmi-uid", "kubevirt.io": "virt-launcher"}},
				"status":   map[string]any{"conditions": []any{map[string]any{"type": "PodScheduled", "status": scheduled, "reason": "Unschedulable", "message": "0/3 nodes are available: 3 Insufficient memory"}}},
			}}
			if scheduled == "missing" {
				pod.SetLabels(map[string]string{"kubevirt.io/created-by": "other-vmi-uid", "kubevirt.io": "virt-launcher"})
			}
			client := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{VMIGVR: "VirtualMachineInstanceList", corev1.SchemeGroupVersion.WithResource("pods"): "PodList"}, pod)
			client.PrependReactor("create", "virtualmachineinstances", func(action k8stesting.Action) (bool, runtime.Object, error) {
				action.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).SetUID("vmi-uid")
				return false, nil, nil
			})
			lookupErr := errors.New("pods forbidden")
			if scheduled == "forbidden" {
				client.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, lookupErr
				})
			}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			spec := VMI{Namespace: "ns", Name: "run-vmi-0", RunID: "run", ContainerDisk: "registry/cirros:1", Memory: "128Mi", CPU: "200m"}
			err := CreateVMIs(ctx, client, []VMI{spec}, 0)
			if err == nil {
				err = WaitRunning(ctx, client, "ns", RunLabel+"=run", []string{spec.Name})
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("error = %v, want deadline exceeded", err)
			}
			if strings.Contains(err.Error(), "Insufficient memory") != (scheduled == "False") {
				t.Fatalf("error = %v, PodScheduled = %s", err, scheduled)
			}
			if errors.Is(err, lookupErr) != (scheduled == "forbidden") {
				t.Fatalf("scheduling lookup error = %v", err)
			}
		})
	}
}

func TestWaitRunningReturnsNilWhenAlreadyRunning(t *testing.T) {
	vmi := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kubevirt.io/v1", "kind": "VirtualMachineInstance",
		"metadata": map[string]any{
			"name": "run-vmi-0", "namespace": "ns", "uid": "vmi-uid",
			"labels": map[string]any{RunLabel: "run"},
		},
		"status": map[string]any{"phase": "Running"},
	}}
	client := dynfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{VMIGVR: "VirtualMachineInstanceList"},
		vmi,
	)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := WaitRunning(ctx, client, "ns", RunLabel+"=run", []string{"run-vmi-0"}); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestWaitRunningSeparatesFailures(t *testing.T) {
	for _, failure := range []string{"none", "startup-timeout", "vmi-failed", "list", "watch", "get-status", "list-pods", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			phase := "Pending"
			if failure == "none" {
				phase = "Running"
			} else if failure == "vmi-failed" {
				phase = "Failed"
			}
			vmi := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "kubevirt.io/v1", "kind": "VirtualMachineInstance",
				"metadata": map[string]any{"name": "vmi", "namespace": "ns", "uid": "uid", "labels": map[string]any{RunLabel: "run"}},
				"status":   map[string]any{"phase": phase},
			}}
			client := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{VMIGVR: "VirtualMachineInstanceList", corev1.SchemeGroupVersion.WithResource("pods"): "PodList"}, vmi)
			apiErr := errors.New("API unavailable")
			switch failure {
			case "list", "get-status", "list-pods":
				verb, target := "list", "virtualmachineinstances"
				if failure == "get-status" {
					verb = "get"
				} else if failure == "list-pods" {
					target = "pods"
				}
				client.PrependReactor(verb, target, func(k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, apiErr
				})
			case "watch":
				client.PrependWatchReactor("virtualmachineinstances", func(k8stesting.Action) (bool, k8swatch.Interface, error) {
					return true, nil, apiErr
				})
			}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			if failure == "cancel" {
				cancel()
			}
			err := WaitRunning(ctx, client, "ns", RunLabel+"=run", []string{"vmi"})
			wantTimeout := failure == "startup-timeout"
			if (err != nil) != (failure != "none") || errors.Is(err, ErrStartupTimeout) != wantTimeout {
				t.Fatalf("err=%v, want startup timeout=%v", err, wantTimeout)
			}
			if wantTimeout && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("err=%v, want startup deadline", err)
			}
			if (failure == "list" || failure == "watch" || failure == "get-status" || failure == "list-pods") && !errors.Is(err, apiErr) {
				t.Fatalf("err=%v, want original API error", err)
			}
			if failure == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("err=%v, want cancellation", err)
			}
		})
	}
}
