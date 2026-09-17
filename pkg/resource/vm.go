package resource

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8swatch "k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"
)

var VMGVR = schema.GroupVersionResource{
	Group:    "kubevirt.io",
	Version:  "v1",
	Resource: "virtualmachines",
}

func CreateAndWaitVMs(ctx context.Context, client dynamic.Interface, count int, namespace, runID, imageID, storageClass string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	eg, ctx := errgroup.WithContext(ctx)
	for i := range count {
		name := fmt.Sprintf("%s-vm-%d", runID, i)
		eg.Go(func() error {
			return createAndWaitVM(ctx, client, name, namespace, runID, imageID, storageClass, timeout)
		})
	}
	return eg.Wait()
}

func createAndWaitVM(ctx context.Context, client dynamic.Interface, name, namespace, runID, imageID, storageClass string, timeout time.Duration) error {
	if _, err := client.Resource(VMGVR).Namespace(namespace).Create(ctx, vmObject(name, namespace, runID, imageID, storageClass), metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create VirtualMachine %s: %w", name, err)
	}
	return WaitVM(ctx, client, namespace, name, timeout)
}

func WaitVM(ctx context.Context, client dynamic.Interface, namespace, name string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	_, err := watchtools.UntilWithSync(ctx, vmListWatch(ctx, client, namespace, name), &unstructured.Unstructured{}, nil, vmCondition(name))
	return err
}

func vmListWatch(ctx context.Context, client dynamic.Interface, namespace, name string) *cache.ListWatch {
	return &cache.ListWatch{
		ListFunc: func(opts metav1.ListOptions) (runtime.Object, error) {
			opts.FieldSelector = "metadata.name=" + name
			return client.Resource(VMGVR).Namespace(namespace).List(ctx, opts)
		},
		WatchFunc: func(opts metav1.ListOptions) (k8swatch.Interface, error) {
			opts.FieldSelector = "metadata.name=" + name
			return client.Resource(VMGVR).Namespace(namespace).Watch(ctx, opts)
		},
	}
}

func vmCondition(name string) watchtools.ConditionFunc {
	return func(event k8swatch.Event) (bool, error) {
		obj, ok := event.Object.(*unstructured.Unstructured)
		if !ok || obj.GetName() != name {
			return false, nil
		}
		if event.Type == k8swatch.Deleted {
			return false, fmt.Errorf("VirtualMachine %s was deleted", name)
		}
		ready, _, _ := unstructured.NestedBool(obj.Object, "status", "ready")
		return ready, nil
	}
}

func vmObject(name, namespace, runID, imageID, storageClass string) *unstructured.Unstructured {
	diskName := name + "-disk"

	volumeClaimTemplates, _ := json.Marshal([]map[string]interface{}{
		{
			"metadata": map[string]interface{}{
				"name": diskName,
				"annotations": map[string]interface{}{
					"harvesterhci.io/imageId": imageID,
				},
			},
			"spec": map[string]interface{}{
				"accessModes":      []interface{}{"ReadWriteMany"},
				"storageClassName": storageClass,
				"volumeMode":       "Block",
				"resources": map[string]interface{}{
					"requests": map[string]interface{}{"storage": "10Gi"},
				},
			},
		},
	})

	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "kubevirt.io/v1",
		"kind":       "VirtualMachine",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
			"labels":    map[string]interface{}{RunLabel: runID},
			"annotations": map[string]interface{}{
				"harvesterhci.io/volumeClaimTemplates": string(volumeClaimTemplates),
			},
		},
		"spec": map[string]interface{}{
			"running": true,
			"template": map[string]interface{}{
				"spec": map[string]interface{}{
					"domain": map[string]interface{}{
						"cpu": map[string]interface{}{
							"cores": 1, "sockets": 1, "threads": 1,
						},
						"resources": map[string]interface{}{
							"requests": map[string]interface{}{
								"memory": "256Mi",
								"cpu":    "125m",
							},
							"limits": map[string]interface{}{
								"memory": "256Mi",
								"cpu":    "1",
							},
						},
						"devices": map[string]interface{}{
							"disks": []interface{}{
								map[string]interface{}{
									"name": "rootdisk",
									"disk": map[string]interface{}{"bus": "virtio"},
								},
							},
						},
					},
					"volumes": []interface{}{
						map[string]interface{}{
							"name":                  "rootdisk",
							"persistentVolumeClaim": map[string]interface{}{"claimName": diskName},
						},
					},
				},
			},
		},
	}}
}
