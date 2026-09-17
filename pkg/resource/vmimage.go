package resource

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8swatch "k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/watch"
)

const RunLabel = "hvperf/run"

var VMImageGVR = schema.GroupVersionResource{
	Group:    "harvesterhci.io",
	Version:  "v1beta1",
	Resource: "virtualmachineimages",
}

func CreateAndWaitVMImage(ctx context.Context, client dynamic.Interface, name, namespace, runID, imageURL string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	lw := vmImageListWatch(ctx, client, namespace, name)

	if _, err := client.Resource(VMImageGVR).Namespace(namespace).Create(ctx, vmImageObject(name, namespace, runID, imageURL), metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create VMImage %s: %w", name, err)
	}

	_, err := watch.UntilWithSync(ctx, lw, &unstructured.Unstructured{}, nil, vmImageCondition(name))
	return err
}

func vmImageListWatch(ctx context.Context, client dynamic.Interface, namespace, name string) *cache.ListWatch {
	return &cache.ListWatch{
		ListFunc: func(opts metav1.ListOptions) (runtime.Object, error) {
			opts.FieldSelector = "metadata.name=" + name
			return client.Resource(VMImageGVR).Namespace(namespace).List(ctx, opts)
		},
		WatchFunc: func(opts metav1.ListOptions) (k8swatch.Interface, error) {
			opts.FieldSelector = "metadata.name=" + name
			return client.Resource(VMImageGVR).Namespace(namespace).Watch(ctx, opts)
		},
	}
}

func vmImageCondition(name string) watch.ConditionFunc {
	return func(event k8swatch.Event) (bool, error) {
		obj, ok := event.Object.(*unstructured.Unstructured)
		if !ok {
			return false, nil
		}
		conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
		for _, c := range conditions {
			cond, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			switch cond["type"] {
			case "Imported":
				if cond["status"] == "True" {
					return true, nil
				}
				if cond["status"] == "False" {
					reason, _, _ := unstructured.NestedString(cond, "reason")
					return false, fmt.Errorf("VMImage %s failed: %s", name, reason)
				}
			case "RetryLimitExceeded":
				if cond["status"] == "True" {
					return false, fmt.Errorf("VMImage %s: retry limit exceeded", name)
				}
			}
		}
		return false, nil
	}
}

func vmImageObject(name, namespace, runID, imageURL string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "harvesterhci.io/v1beta1",
		"kind":       "VirtualMachineImage",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
			"labels":    map[string]interface{}{RunLabel: runID},
		},
		"spec": map[string]interface{}{
			"displayName": name,
			"sourceType":  "download",
			"url":         imageURL,
		},
	}}
}
