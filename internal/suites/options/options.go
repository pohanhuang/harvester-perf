package options

import (
	"encoding/json"
	"fmt"

	"github.com/harvester/hvperf/pkg/suites"
	"go.yaml.in/yaml/v4"
)

// FromOptions converts the given suites.Options to the specific options type V.
func FromOptions[V any](opts *suites.Options) (V, error) {
	var o V
	b, err := json.Marshal(opts)
	if err != nil {
		return o, err
	}

	if err := json.Unmarshal(b, &o); err != nil {
		return o, err
	}
	return o, nil
}

// DecodeSection extracts the named key from opts and YAML-decodes it into V.
func DecodeSection[V any](opts suites.Options, key string) (V, error) {
	var zero V
	raw, ok := opts[key]
	if !ok {
		return zero, fmt.Errorf("%s options not provided", key)
	}
	b, err := yaml.Marshal(raw)
	if err != nil {
		return zero, fmt.Errorf("%s options: %w", key, err)
	}
	var o V
	if err := yaml.Unmarshal(b, &o); err != nil {
		return zero, fmt.Errorf("%s options: %w", key, err)
	}
	return o, nil
}
