package scenarios

import (
	"fmt"
	"sort"
	"strings"

	"github.com/harvester/benchctl/scenarios/api"
	"github.com/harvester/benchctl/scenarios/harvestervmcapacity"
	"github.com/harvester/benchctl/scenarios/harvestervmcreate"
	"github.com/harvester/benchctl/scenarios/helloworld"
	"github.com/harvester/benchctl/scenarios/platformbaseline"
	"github.com/harvester/benchctl/scenarios/resourceusage"
	"k8s.io/perf-tests/clusterloader2/pkg/measurement"
)

var registry = map[string]api.Scenario{}

func init() {
	register(
		helloworld.New(),
		resourceusage.New(),
		platformbaseline.New(),
		harvestervmcapacity.New(),
		harvestervmcreate.New(),
	)
}

func Run(name string, ctx api.Context) ([]measurement.Summary, error) {
	scenario, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown scenario %q; available scenarios: %s", name, strings.Join(Names(), ", "))
	}
	return scenario.Run(ctx)
}

func Names() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func register(scenarios ...api.Scenario) {
	for _, scenario := range scenarios {
		registry[scenario.Name()] = scenario
	}
}
