package harvestervmcapacity

import (
	"github.com/harvester/benchctl/scenarios/api"
	"github.com/harvester/benchctl/scenarios/harvestervm"
)

func configFromContext(ctx api.Context) harvestervm.Config {
	cfg := harvestervm.DefaultConfig()
	cfg.NamePrefix = "vmcapacity"
	cfg.Steps = defaultSteps()

	harvestervm.ApplyCapacityConfig(&cfg, ctx.Config, defaultSteps())
	return cfg
}

func defaultSteps() []string {
	return []string{"prepare-image", "create-batches", "cleanup-vms"}
}
