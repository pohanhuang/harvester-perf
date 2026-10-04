package resource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	k8swatch "k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"

	kubevirtv1 "kubevirt.io/api/core/v1"
)

var VMIGVR = kubevirtv1.SchemeGroupVersion.WithResource("virtualmachineinstances")

const RunLabel = "hvperf/run"

// VMI is the containerdisk workload shape used by the density suite.
type VMI struct {
	Namespace     string `yaml:"-"`
	Name          string `yaml:"-"`
	RunID         string `yaml:"-"`
	ContainerDisk string `yaml:"containerDisk"`
	Memory        string `yaml:"memory"`
	CPU           string `yaml:"cpu"`
}

// CreateVMIs creates VMIs with rate control.
func CreateVMIs(ctx context.Context, client dynamic.Interface, vmis []VMI, interval time.Duration) error {
	for i, vmi := range vmis {
		if i > 0 && interval > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(interval):
			}
		}
		obj, err := vmiObject(vmi)
		if err != nil {
			return err
		}
		if _, err := client.Resource(VMIGVR).Namespace(vmi.Namespace).Create(ctx, obj, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create VMI %s: %w", vmi.Name, err)
		}
	}
	return nil
}

// WaitRunning watches VMIs by label selector until all named VMIs are Running.
func WaitRunning(ctx context.Context, client dynamic.Interface, ns, selector string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	pending := make(map[string]bool, len(names))
	for _, name := range names {
		pending[name] = true
	}

	lw := labelListWatch(ctx, client, ns, selector)
	_, err := watchtools.UntilWithSync(ctx, cache.ToListWatcherWithWatchListSemantics(lw, client),
		&unstructured.Unstructured{}, nil, vmiRunningCondition(ctx, client, ns, pending))
	if err == nil {
		return nil
	}

	err = errors.Join(err, ctx.Err())
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		for name := range pending {
			probeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			if vmiObj, getErr := client.Resource(VMIGVR).Namespace(ns).Get(probeCtx, name, metav1.GetOptions{}); getErr == nil {
				err = errors.Join(err, vmiSchedulingError(probeCtx, client, ns, string(vmiObj.GetUID())))
			}
			cancel()
		}
	}
	return fmt.Errorf("%d/%d VMIs not Running: %w", len(pending), len(names), err)
}

func vmiRunningCondition(ctx context.Context, client dynamic.Interface, ns string, pending map[string]bool) watchtools.ConditionFunc {
	return func(event k8swatch.Event) (bool, error) {
		obj, ok := event.Object.(*unstructured.Unstructured)
		if !ok || !pending[obj.GetName()] {
			return false, nil
		}
		if event.Type == k8swatch.Deleted {
			return false, fmt.Errorf("VMI %s was deleted", obj.GetName())
		}
		var vmi kubevirtv1.VirtualMachineInstance
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &vmi); err != nil {
			return false, fmt.Errorf("convert VMI %s: %w", obj.GetName(), err)
		}
		switch vmi.Status.Phase {
		case kubevirtv1.Running:
			delete(pending, obj.GetName())
			return len(pending) == 0, nil
		case kubevirtv1.Failed:
			probeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			schedErr := vmiSchedulingError(probeCtx, client, ns, string(obj.GetUID()))
			cancel()
			return false, errors.Join(fmt.Errorf("VMI %s failed", obj.GetName()), schedErr)
		}
		return false, nil
	}
}

func labelListWatch(ctx context.Context, client dynamic.Interface, ns, selector string) *cache.ListWatch {
	return &cache.ListWatch{
		ListFunc: func(opts metav1.ListOptions) (runtime.Object, error) {
			opts.LabelSelector = selector
			return client.Resource(VMIGVR).Namespace(ns).List(ctx, opts)
		},
		WatchFunc: func(opts metav1.ListOptions) (k8swatch.Interface, error) {
			opts.LabelSelector = selector
			return client.Resource(VMIGVR).Namespace(ns).Watch(ctx, opts)
		},
	}
}

// Read current scheduling status, not historical FailedScheduling events.
func vmiSchedulingError(ctx context.Context, client dynamic.Interface, namespace, uid string) error {
	pods, err := client.Resource(corev1.SchemeGroupVersion.WithResource("pods")).Namespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "kubevirt.io=virt-launcher,kubevirt.io/created-by=" + uid,
	})
	if err != nil {
		return fmt.Errorf("read launcher scheduling status: %w", err)
	}
	for _, obj := range pods.Items {
		var pod corev1.Pod
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &pod); err != nil {
			return fmt.Errorf("decode launcher pod %s: %w", obj.GetName(), err)
		}
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionFalse {
				return fmt.Errorf("launcher pod %s: %s: %s", pod.Name, condition.Reason, condition.Message)
			}
		}
	}
	return nil
}

func vmiObject(vmi VMI) (*unstructured.Unstructured, error) {
	memory, err := resource.ParseQuantity(vmi.Memory)
	if err != nil {
		return nil, fmt.Errorf("memory: %w", err)
	}
	cpu, err := resource.ParseQuantity(vmi.CPU)
	if err != nil {
		return nil, fmt.Errorf("cpu: %w", err)
	}
	obj := &kubevirtv1.VirtualMachineInstance{
		TypeMeta:   metav1.TypeMeta{APIVersion: kubevirtv1.SchemeGroupVersion.String(), Kind: "VirtualMachineInstance"},
		ObjectMeta: metav1.ObjectMeta{Namespace: vmi.Namespace, Name: vmi.Name, Labels: map[string]string{RunLabel: vmi.RunID}},
		Spec: kubevirtv1.VirtualMachineInstanceSpec{
			Domain: kubevirtv1.DomainSpec{
				Resources: kubevirtv1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceMemory: memory, corev1.ResourceCPU: cpu}, Limits: corev1.ResourceList{corev1.ResourceMemory: memory, corev1.ResourceCPU: cpu}},
				Devices:   kubevirtv1.Devices{Disks: []kubevirtv1.Disk{{Name: "rootdisk", DiskDevice: kubevirtv1.DiskDevice{Disk: &kubevirtv1.DiskTarget{Bus: kubevirtv1.DiskBusVirtio}}}}},
			},
			Volumes: []kubevirtv1.Volume{{Name: "rootdisk", VolumeSource: kubevirtv1.VolumeSource{ContainerDisk: &kubevirtv1.ContainerDiskSource{Image: vmi.ContainerDisk, ImagePullPolicy: corev1.PullIfNotPresent}}}},
		},
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("marshal VMI %s: %w", vmi.Name, err)
	}
	u := &unstructured.Unstructured{}
	if err := u.UnmarshalJSON(data); err != nil {
		return nil, fmt.Errorf("decode VMI %s: %w", vmi.Name, err)
	}
	return u, nil
}
