package harvestervm

import (
	"fmt"
	"strconv"
	"time"

	"github.com/harvester/benchctl/scenarios/api"
)

const (
	defaultCount                         = 5
	defaultBatchSize                     = 5
	defaultMaxVMs                        = 50
	defaultNamespace                     = "default"
	defaultImage                         = "quay.io/kubevirt/cirros-container-disk-demo:latest"
	defaultImageMode                     = "containerDisk"
	defaultCPUCores                      = 1
	defaultCPUSockets                    = 1
	defaultCPUThreads                    = 1
	defaultCPUMaxSockets                 = 4
	defaultMemory                        = "64Mi"
	defaultDiskBus                       = "virtio"
	defaultRunStrategy                   = "Always"
	defaultNamePrefix                    = "vmcreate"
	defaultTimeout                       = 20 * time.Minute
	defaultPollInterval                  = 5 * time.Second
	defaultTerminationGracePeriodSeconds = 0
)

type Config struct {
	Count                         int
	BatchSize                     int
	MaxVMs                        int
	Namespace                     string
	Image                         string
	ImageMode                     string
	CPUCores                      int64
	CPUSockets                    int64
	CPUThreads                    int64
	CPUMaxSockets                 int64
	Memory                        string
	DiskBus                       string
	RunStrategy                   string
	NamePrefix                    string
	Timeout                       time.Duration
	PollInterval                  time.Duration
	TerminationGracePeriodSeconds int64
	Cleanup                       bool
	Steps                         []string
}

func DefaultConfig() Config {
	return Config{
		Count:                         defaultCount,
		BatchSize:                     defaultBatchSize,
		MaxVMs:                        defaultMaxVMs,
		Namespace:                     defaultNamespace,
		Image:                         defaultImage,
		ImageMode:                     defaultImageMode,
		CPUCores:                      defaultCPUCores,
		CPUSockets:                    defaultCPUSockets,
		CPUThreads:                    defaultCPUThreads,
		CPUMaxSockets:                 defaultCPUMaxSockets,
		Memory:                        defaultMemory,
		DiskBus:                       defaultDiskBus,
		RunStrategy:                   defaultRunStrategy,
		NamePrefix:                    defaultNamePrefix,
		Timeout:                       defaultTimeout,
		PollInterval:                  defaultPollInterval,
		TerminationGracePeriodSeconds: defaultTerminationGracePeriodSeconds,
		Cleanup:                       true,
	}
}

func ApplyCreateConfig(cfg *Config, scenarioConfig *api.ConfigFile, steps []string) {
	applyCommonConfig(cfg, scenarioConfig, steps)
	if step, ok := scenarioConfig.Step("create-vms"); ok {
		applyVMConfig(cfg, step.Config)
		cfg.Count = intConfig(step.Config, "count", cfg.Count)
	}
}

func ApplyCapacityConfig(cfg *Config, scenarioConfig *api.ConfigFile, steps []string) {
	applyCommonConfig(cfg, scenarioConfig, steps)
	if step, ok := scenarioConfig.Step("create-batches"); ok {
		applyVMConfig(cfg, step.Config)
		cfg.BatchSize = intConfig(step.Config, "batchSize", cfg.BatchSize)
		cfg.MaxVMs = intConfig(step.Config, "maxVMs", cfg.MaxVMs)
		cfg.Timeout = durationConfig(step.Config, "waitTimeoutPerBatch", cfg.Timeout)
		cfg.PollInterval = durationConfig(step.Config, "pollInterval", cfg.PollInterval)
	}
}

func applyCommonConfig(cfg *Config, scenarioConfig *api.ConfigFile, steps []string) {
	if scenarioConfig == nil {
		cfg.Steps = steps
		return
	}
	if len(scenarioConfig.Steps) > 0 {
		cfg.Steps = make([]string, 0, len(scenarioConfig.Steps))
		for _, step := range scenarioConfig.Steps {
			cfg.Steps = append(cfg.Steps, step.Name)
		}
	} else {
		cfg.Steps = steps
	}

	if step, ok := scenarioConfig.Step("prepare-image"); ok {
		cfg.ImageMode = stringConfig(step.Config, "mode", cfg.ImageMode)
		cfg.Image = stringConfig(step.Config, "image", cfg.Image)
	}
	if step, ok := scenarioConfig.Step("wait-running"); ok {
		cfg.Timeout = durationConfig(step.Config, "timeout", cfg.Timeout)
		cfg.PollInterval = durationConfig(step.Config, "pollInterval", cfg.PollInterval)
	}
	if step, ok := scenarioConfig.Step("cleanup-vms"); ok {
		cfg.Cleanup = boolConfig(step.Config, "enabled", cfg.Cleanup)
	}
}

func applyVMConfig(cfg *Config, values map[string]interface{}) {
	cfg.Namespace = stringConfig(values, "namespace", cfg.Namespace)
	cfg.Image = stringConfig(values, "image", cfg.Image)
	cfg.CPUCores = int64Config(values, "cpuCores", cfg.CPUCores)
	cfg.CPUSockets = int64Config(values, "cpuSockets", cfg.CPUSockets)
	cfg.CPUThreads = int64Config(values, "cpuThreads", cfg.CPUThreads)
	cfg.CPUMaxSockets = int64Config(values, "cpuMaxSockets", cfg.CPUMaxSockets)
	cfg.Memory = stringConfig(values, "memory", cfg.Memory)
	cfg.DiskBus = stringConfig(values, "diskBus", cfg.DiskBus)
	cfg.RunStrategy = stringConfig(values, "runStrategy", cfg.RunStrategy)
	cfg.NamePrefix = stringConfig(values, "namePrefix", cfg.NamePrefix)
	cfg.TerminationGracePeriodSeconds = int64Config(values, "terminationGracePeriodSeconds", cfg.TerminationGracePeriodSeconds)
}

func int64Config(values map[string]interface{}, key string, fallback int64) int64 {
	return int64(intConfig(values, key, int(fallback)))
}

func stringConfig(values map[string]interface{}, key, fallback string) string {
	if values == nil {
		return fallback
	}
	value, ok := values[key]
	if !ok || value == nil {
		return fallback
	}
	if text, ok := value.(string); ok && text != "" {
		return text
	}
	return fallback
}

func intConfig(values map[string]interface{}, key string, fallback int) int {
	if values == nil {
		return fallback
	}
	value, ok := values[key]
	if !ok || value == nil {
		return fallback
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		parsed, err := strconv.Atoi(typed)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func boolConfig(values map[string]interface{}, key string, fallback bool) bool {
	if values == nil {
		return fallback
	}
	value, ok := values[key]
	if !ok || value == nil {
		return fallback
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		parsed, err := strconv.ParseBool(typed)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func durationConfig(values map[string]interface{}, key string, fallback time.Duration) time.Duration {
	if values == nil {
		return fallback
	}
	value, ok := values[key]
	if !ok || value == nil {
		return fallback
	}
	switch typed := value.(type) {
	case time.Duration:
		return typed
	case string:
		parsed, err := time.ParseDuration(typed)
		if err == nil {
			return parsed
		}
	case int:
		return time.Duration(typed) * time.Second
	case int64:
		return time.Duration(typed) * time.Second
	case float64:
		return time.Duration(typed) * time.Second
	}
	fmt.Printf("Ignoring invalid duration config %s=%v\n", key, value)
	return fallback
}
