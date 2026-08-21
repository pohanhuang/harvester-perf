package api

import (
	"time"

	"k8s.io/perf-tests/clusterloader2/pkg/framework"
	"k8s.io/perf-tests/clusterloader2/pkg/measurement"
)

type Context struct {
	Framework    *framework.Framework
	Measurements measurement.Manager
	Config       *ConfigFile

	PlatformBaselineDuration time.Duration
	PlatformBaselineInterval time.Duration

	VMCreateCount     int
	VMCreateNamespace string
	VMCreateImage     string
	VMCreateTimeout   time.Duration
	VMCreateCleanup   bool
}

type Scenario interface {
	Name() string
	Run(Context) ([]measurement.Summary, error)
}
