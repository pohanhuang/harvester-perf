package harvestervmcreate

import (
	"time"

	"github.com/harvester/benchctl/scenarios/api"
	"github.com/harvester/benchctl/scenarios/harvestervm"
)

func configFromContext(ctx api.Context) harvestervm.Config {
	cfg := harvestervm.DefaultConfig()
	cfg.Steps = defaultSteps()

	if ctx.VMCreateCount > 0 {
		cfg.Count = ctx.VMCreateCount
	}
	if ctx.VMCreateNamespace != "" {
		cfg.Namespace = ctx.VMCreateNamespace
	}
	if ctx.VMCreateImage != "" {
		cfg.Image = ctx.VMCreateImage
	}
	if ctx.VMCreateTimeout > 0 {
		cfg.Timeout = ctx.VMCreateTimeout
	}
	cfg.Cleanup = ctx.VMCreateCleanup
	if ctx.VMCreateTimeout <= 0 {
		cfg.Timeout = 20 * time.Minute
	}

	harvestervm.ApplyCreateConfig(&cfg, ctx.Config, defaultSteps())
	return cfg
}

func defaultSteps() []string {
	return []string{"prepare-image", "create-vms", "wait-running", "cleanup-vms"}
}
