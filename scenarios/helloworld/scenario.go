package helloworld

import (
	"fmt"
	"time"

	"github.com/harvester/benchctl/scenarios/api"
	"k8s.io/perf-tests/clusterloader2/pkg/measurement"
)

const name = "hello"

type Scenario struct{}

func New() *Scenario {
	return &Scenario{}
}

func (s *Scenario) Name() string {
	return name
}

func (s *Scenario) Run(ctx api.Context) ([]measurement.Summary, error) {
	if err := ctx.Measurements.Execute("HelloWorld", "demo", map[string]interface{}{
		"action":  "start",
		"message": "Hello from BenchCTL!",
	}); err != nil {
		return nil, fmt.Errorf("starting HelloWorld: %w", err)
	}

	fmt.Println("Simulating work for 3 seconds...")
	time.Sleep(3 * time.Second)

	if err := ctx.Measurements.Execute("HelloWorld", "demo", map[string]interface{}{
		"action": "gather",
	}); err != nil {
		return nil, fmt.Errorf("gathering HelloWorld: %w", err)
	}

	return nil, nil
}
