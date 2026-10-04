package cmd

import (
	"fmt"
	"os"

	"github.com/harvester/hvperf/pkg/suites"
	"go.yaml.in/yaml/v4"
)

func loadRunOptions(path string) (suites.Options, error) {
	result := *suites.DefaultGlobalOptions()
	if path == "" {
		return result, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var override suites.Options
	if err := yaml.Unmarshal(data, &override); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	for k, v := range override {
		result[k] = v
	}
	return result, nil
}
