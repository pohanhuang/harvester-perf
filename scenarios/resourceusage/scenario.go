package resourceusage

import (
	"fmt"
	"time"

	"github.com/harvester/benchctl/scenarios/api"
	"k8s.io/perf-tests/clusterloader2/pkg/measurement"
)

const name = "resource-usage"

type Scenario struct{}

func New() *Scenario {
	return &Scenario{}
}

func (s *Scenario) Name() string {
	return name
}

func (s *Scenario) Run(ctx api.Context) ([]measurement.Summary, error) {
	fmt.Println("Starting ResourceUsageSummary measurement...")
	if err := ctx.Measurements.Execute("ResourceUsageSummary", "test1", map[string]interface{}{
		"action":    "start",
		"namespace": "",
	}); err != nil {
		return nil, fmt.Errorf("starting ResourceUsageSummary: %w", err)
	}

	fmt.Println("Collecting metrics for 30 seconds...")
	time.Sleep(30 * time.Second)

	fmt.Println("Gathering results...")
	if err := ctx.Measurements.Execute("ResourceUsageSummary", "test1", map[string]interface{}{
		"action": "gather",
	}); err != nil {
		return nil, fmt.Errorf("gathering ResourceUsageSummary: %w", err)
	}

	return nil, nil
}
