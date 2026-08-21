package harvestervm

import (
	"context"
	"fmt"
	"sort"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var (
	VirtualMachineGVR = schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachines",
	}
	VirtualMachineInstanceGVR = schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachineinstances",
	}
)

type Result struct {
	Name              string
	CreatedAt         time.Time
	APICreateDuration time.Duration
	RunningAt         time.Time
	RunningDuration   time.Duration
	Phase             string
}

type CPUReport struct {
	Cores      int64
	Sockets    int64
	Threads    int64
	MaxSockets int64
}

type DurationStats struct {
	MinMS int64
	P50MS int64
	P95MS int64
	P99MS int64
	MaxMS int64
	AvgMS int64
}

func PrepareImage(config Config) (string, error) {
	if config.ImageMode != "containerDisk" {
		return "", fmt.Errorf("unsupported image mode %q; currently supported: containerDisk", config.ImageMode)
	}
	return fmt.Sprintf("Using containerDisk image %s; no Harvester VirtualMachineImage resource is created in containerDisk mode", config.Image), nil
}

func CreateVMs(ctx context.Context, client dynamic.Interface, config Config, runID string, startIndex, count int) (map[string]*Result, error) {
	results := make(map[string]*Result, count)
	for i := 0; i < count; i++ {
		vmName := fmt.Sprintf("%s-%03d", runID, startIndex+i)
		vm := BuildVM(config, vmName, runID)

		start := time.Now().UTC()
		if _, err := client.Resource(VirtualMachineGVR).Namespace(config.Namespace).Create(ctx, vm, metav1.CreateOptions{}); err != nil {
			return nil, fmt.Errorf("creating VM %s: %w", vmName, err)
		}
		results[vmName] = &Result{
			Name:              vmName,
			CreatedAt:         start,
			APICreateDuration: time.Since(start),
		}
	}
	return results, nil
}

func WaitForVMIsRunning(ctx context.Context, client dynamic.Interface, namespace, runID string, results map[string]*Result, timeout, pollInterval time.Duration) error {
	labelSelector := "benchctl.harvester.io/run-id=" + runID
	deadline := time.Now().Add(timeout)

	for {
		list, err := client.Resource(VirtualMachineInstanceGVR).Namespace(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			return err
		}

		for i := range list.Items {
			item := &list.Items[i]
			result, ok := results[item.GetName()]
			if !ok {
				continue
			}
			phase, _, _ := unstructured.NestedString(item.Object, "status", "phase")
			result.Phase = phase
			if phase == "Running" && result.RunningAt.IsZero() {
				result.RunningAt = time.Now().UTC()
				result.RunningDuration = result.RunningAt.Sub(result.CreatedAt)
			}
		}

		if CountRunning(results) == len(results) {
			return nil
		}
		if time.Now().After(deadline) {
			fmt.Println("Timed out waiting for all VMIs to become Running; writing partial results.")
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

func CountRunning(results map[string]*Result) int {
	count := 0
	for _, result := range results {
		if !result.RunningAt.IsZero() {
			count++
		}
	}
	return count
}

func MergeResults(dst, src map[string]*Result) {
	for name, result := range src {
		dst[name] = result
	}
}

func CleanupVMs(ctx context.Context, client dynamic.Interface, namespace, runID string) error {
	list, err := client.Resource(VirtualMachineGVR).Namespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "benchctl.harvester.io/run-id=" + runID,
	})
	if err != nil {
		return err
	}
	for i := range list.Items {
		name := list.Items[i].GetName()
		if err := client.Resource(VirtualMachineGVR).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
			return fmt.Errorf("deleting VM %s: %w", name, err)
		}
	}
	return nil
}

func BuildVM(config Config, name, runID string) *unstructured.Unstructured {
	labels := map[string]interface{}{
		"benchctl.harvester.io/scenario": config.NamePrefix,
		"benchctl.harvester.io/run-id":   runID,
	}

	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "kubevirt.io/v1",
		"kind":       "VirtualMachine",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": config.Namespace,
			"labels":    labels,
		},
		"spec": map[string]interface{}{
			"runStrategy": config.RunStrategy,
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": labels,
				},
				"spec": map[string]interface{}{
					"terminationGracePeriodSeconds": config.TerminationGracePeriodSeconds,
					"domain": map[string]interface{}{
						"cpu": map[string]interface{}{
							"cores":      config.CPUCores,
							"sockets":    config.CPUSockets,
							"threads":    config.CPUThreads,
							"maxSockets": config.CPUMaxSockets,
						},
						"memory": map[string]interface{}{
							"guest": config.Memory,
						},
						"resources": map[string]interface{}{
							"requests": map[string]interface{}{
								"memory": config.Memory,
							},
							"limits": map[string]interface{}{
								"memory": config.Memory,
							},
						},
						"devices": map[string]interface{}{
							"disks": []interface{}{
								map[string]interface{}{
									"name": "containerdisk",
									"disk": map[string]interface{}{
										"bus": config.DiskBus,
									},
								},
							},
						},
					},
					"volumes": []interface{}{
						map[string]interface{}{
							"name": "containerdisk",
							"containerDisk": map[string]interface{}{
								"image": config.Image,
							},
						},
					},
				},
			},
		},
	}}
}

func DurationStatsFrom(values []time.Duration) DurationStats {
	if len(values) == 0 {
		return DurationStats{}
	}

	sort.Slice(values, func(i, j int) bool {
		return values[i] < values[j]
	})

	var total time.Duration
	for _, value := range values {
		total += value
	}

	return DurationStats{
		MinMS: values[0].Milliseconds(),
		P50MS: percentile(values, 50).Milliseconds(),
		P95MS: percentile(values, 95).Milliseconds(),
		P99MS: percentile(values, 99).Milliseconds(),
		MaxMS: values[len(values)-1].Milliseconds(),
		AvgMS: (total / time.Duration(len(values))).Milliseconds(),
	}
}

func percentile(values []time.Duration, pct int) time.Duration {
	if len(values) == 0 {
		return 0
	}
	index := (pct*len(values) + 99) / 100
	if index <= 0 {
		index = 1
	}
	if index > len(values) {
		index = len(values)
	}
	return values[index-1]
}
