package suites

import (
	// register built-in suites
	_ "github.com/harvester/hvperf/internal/suites/etcd"
	_ "github.com/harvester/hvperf/internal/suites/nodes"
	_ "github.com/harvester/hvperf/internal/suites/resourcefootprint"
	_ "github.com/harvester/hvperf/internal/suites/density"
)
