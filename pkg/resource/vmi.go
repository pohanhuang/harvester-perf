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
	"k8s.io/apimachinery/pkg/util/wait"
	k8swatch "k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"

	kubevirtv1 "kubevirt.io/api/core/v1"
)

var VMIGVR = kubevirtv1.SchemeGroupVersion.WithResource("virtualmachineinstances")

const RunLabel = "hvperf/run"

// ErrStartupTimeout marks a clean startup deadline with no API errors observed.
var ErrStartupTimeout = errors.New("VMI startup timed out")

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
// A clean startup deadline wraps ErrStartupTimeout; other failures do not.
func WaitRunning(ctx context.Context, client dynamic.Interface, ns, selector string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	pending := make(map[string]bool, len(names))
	for _, name := range names {
		pending[name] = true
	}

	lw, apiErrors := labelListWatch(ctx, client, ns, selector)
	_, err := watchtools.UntilWithSync(ctx, cache.ToListWatcherWithWatchListSemantics(lw, client),
		&unstructured.Unstructured{}, nil, vmiRunningCondition(ctx, client, ns, pending))
	if err == nil {
		return nil
	}

	timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded) && wait.Interrupted(err)
	var apiErr error
	select {
	case apiErr = <-apiErrors:
	default:
	}
	err = errors.Join(err, ctx.Err(), apiErr)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		schedErrs, probeAPIErrs := probeStartupErrors(ctx, client, ns, pending)
		apiErr = errors.Join(apiErr, probeAPIErrs)
		err = errors.Join(err, schedErrs, probeAPIErrs)
	}
	err = fmt.Errorf("%d/%d VMIs not Running: %w", len(pending), len(names), err)
	if timedOut && apiErr == nil {
		return fmt.Errorf("%w: %w", ErrStartupTimeout, err)
	}
	return err
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
			schedErr, schedRunErr := vmiSchedulingError(probeCtx, client, ns, string(obj.GetUID()))
			cancel()
			return false, errors.Join(fmt.Errorf("VMI %s failed", obj.GetName()), schedErr, schedRunErr)
		}
		return false, nil
	}
}

func labelListWatch(ctx context.Context, client dynamic.Interface, ns, selector string) (*cache.ListWatch, <-chan error) {
	// UntilWithSync retries API errors; retain one so a timeout preserves its cause.
	apiErrors := make(chan error, 1)
	record := func(err error) {
		if err != nil {
			select {
			case apiErrors <- err:
			default:
			}
		}
	}
	res := client.Resource(VMIGVR).Namespace(ns)
	return &cache.ListWatch{
		ListFunc: func(opts metav1.ListOptions) (runtime.Object, error) {
			opts.LabelSelector = selector
			obj, err := res.List(ctx, opts)
			record(err)
			return obj, err
		},
		WatchFunc: func(opts metav1.ListOptions) (k8swatch.Interface, error) {
			opts.LabelSelector = selector
			watcher, err := res.Watch(ctx, opts)
			record(err)
			return watcher, err
		},
	}, apiErrors
}

func probeStartupErrors(ctx context.Context, client dynamic.Interface, ns string, pending map[string]bool) (schedErrs, apiErrs error) {
	res := client.Resource(VMIGVR).Namespace(ns)
	for name := range pending {
		probeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		vmiObj, err := res.Get(probeCtx, name, metav1.GetOptions{})
		if err != nil {
			apiErrs = errors.Join(apiErrs, fmt.Errorf("read VMI %s startup status: %w", name, err))
			cancel()
			continue
		}
		schedErr, runErr := vmiSchedulingError(probeCtx, client, ns, string(vmiObj.GetUID()))
		schedErrs = errors.Join(schedErrs, schedErr)
		apiErrs = errors.Join(apiErrs, runErr)
		cancel()
	}
	return
}

// Read current scheduling status, not historical FailedScheduling events.
func vmiSchedulingError(ctx context.Context, client dynamic.Interface, namespace, uid string) (schedulingErr, runErr error) {
	pods, err := client.Resource(corev1.SchemeGroupVersion.WithResource("pods")).Namespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "kubevirt.io=virt-launcher,kubevirt.io/created-by=" + uid,
	})
	if err != nil {
		return nil, fmt.Errorf("read launcher scheduling status: %w", err)
	}
	for _, obj := range pods.Items {
		var pod corev1.Pod
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &pod); err != nil {
			return nil, fmt.Errorf("decode launcher pod %s: %w", obj.GetName(), err)
		}
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionFalse {
				return fmt.Errorf("launcher pod %s: %s: %s", pod.Name, condition.Reason, condition.Message), nil
			}
		}
	}
	return nil, nil
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
