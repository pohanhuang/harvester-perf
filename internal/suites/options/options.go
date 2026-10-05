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

// DecodeSection YAML-decodes the named key from opts over def; fields absent from the section keep def's values.
func DecodeSection[V any](opts suites.Options, key string, def V) (V, error) {
	raw, ok := opts[key]
	if !ok {
		return def, nil
	}
	b, err := yaml.Marshal(raw)
	if err != nil {
		return def, fmt.Errorf("%s options: %w", key, err)
	}
	o := def
	if err := yaml.Unmarshal(b, &o); err != nil {
		return def, fmt.Errorf("%s options: %w", key, err)
	}
	return o, nil
}
