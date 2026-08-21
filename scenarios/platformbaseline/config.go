package platformbaseline

import (
	"time"

	"github.com/harvester/benchctl/scenarios/api"
)

const (
	defaultDuration = 30 * time.Second
	defaultInterval = 10 * time.Second
)

type config struct {
	Duration time.Duration
	Interval time.Duration
}

func configFromContext(ctx api.Context) config {
	return config{
		Duration: durationOrDefault(ctx.PlatformBaselineDuration, defaultDuration),
		Interval: durationOrDefault(ctx.PlatformBaselineInterval, defaultInterval),
	}
}

func durationOrDefault(value, defaultValue time.Duration) time.Duration {
	if value <= 0 {
		return defaultValue
	}
	return value
}
