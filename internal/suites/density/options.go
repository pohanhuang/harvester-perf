package density

import (
	"errors"
	"fmt"
	"time"

	"github.com/harvester/hvperf/pkg/resource"
)

// Options is the density block of hvperf.yaml.
type Options struct {
	BatchSize        int           `yaml:"batchSize"`
	MaxVMs           int           `yaml:"maxVMs"`
	BatchWaitTimeout time.Duration `yaml:"batchWaitTimeout"`
	VMI              resource.VMI  `yaml:"vmi"`
}

func DefaultOptions() Options {
	return Options{
		BatchSize:        10,
		BatchWaitTimeout: 5 * time.Minute,
		VMI: resource.VMI{
			ContainerDisk: "quay.io/kubevirt/cirros-container-disk-demo:latest",
			Memory:        "90Mi",
			CPU:           "100m",
		},
	}
}

func (o Options) Validate() error {
	if o.MaxVMs < 0 {
		return fmt.Errorf("maxVMs must be non-negative, got %d", o.MaxVMs)
	}
	if o.BatchSize <= 0 {
		return fmt.Errorf("batchSize must be positive, got %d", o.BatchSize)
	}
	if o.BatchWaitTimeout <= 0 {
		return fmt.Errorf("batchWaitTimeout must be positive, got %s", o.BatchWaitTimeout)
	}
	if o.VMI.ContainerDisk == "" {
		return errors.New("vmi.containerDisk must not be empty")
	}
	return nil
}
