package platformbaseline

import "time"

const (
	defaultDuration = 30 * time.Second
	defaultInterval = 10 * time.Second
)

type measurementConfig struct {
	Duration time.Duration
	Interval time.Duration
}

func configFromParams(params map[string]interface{}) measurementConfig {
	return measurementConfig{
		Duration: durationParam(params["duration"], defaultDuration),
		Interval: durationParam(params["interval"], defaultInterval),
	}
}

func durationParam(value interface{}, defaultValue time.Duration) time.Duration {
	duration, ok := value.(time.Duration)
	if !ok || duration <= 0 {
		return defaultValue
	}
	return duration
}
