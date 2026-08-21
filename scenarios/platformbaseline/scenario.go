package platformbaseline

import (
	"fmt"

	"github.com/harvester/benchctl/scenarios/api"
	"k8s.io/perf-tests/clusterloader2/pkg/measurement"
)

const name = "platform-baseline"

type Scenario struct{}

func New() *Scenario {
	return &Scenario{}
}

func (s *Scenario) Name() string {
	return name
}

func (s *Scenario) Run(ctx api.Context) ([]measurement.Summary, error) {
	config := configFromContext(ctx)

	fmt.Printf("Gathering platform baseline metrics for %s at %s intervals...\n", config.Duration, config.Interval)
	if err := ctx.Measurements.Execute("PlatformBaseline", "phase1", map[string]interface{}{
		"action":   "gather",
		"duration": config.Duration,
		"interval": config.Interval,
	}); err != nil {
		return nil, fmt.Errorf("gathering PlatformBaseline: %w", err)
	}

	return nil, nil
}
