package api

import (
	"embed"
	"fmt"
	"os"
	"strings"

	"sigs.k8s.io/yaml"
)

//go:embed builtin-configs/*.yaml
var builtinConfigs embed.FS

type ConfigFile struct {
	Name  string       `json:"name" yaml:"name"`
	Steps []StepConfig `json:"steps" yaml:"steps"`
}

type StepConfig struct {
	Name   string                 `json:"name" yaml:"name"`
	Config map[string]interface{} `json:"config" yaml:"config"`
}

func LoadConfigFile(path string) (*ConfigFile, error) {
	if strings.HasPrefix(path, "builtin:") {
		return LoadBuiltinConfig(strings.TrimPrefix(path, "builtin:"))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	return decodeConfig(path, data)
}

func LoadBuiltinConfig(name string) (*ConfigFile, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("builtin scenario config name is required")
	}
	if !strings.HasSuffix(name, ".yaml") {
		name += ".yaml"
	}

	path := "builtin-configs/" + name
	data, err := builtinConfigs.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read builtin scenario config %s: %w", name, err)
	}
	return decodeConfig("builtin:"+name, data)
}

func decodeConfig(source string, data []byte) (*ConfigFile, error) {
	var config ConfigFile
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("decode scenario config %s: %w", source, err)
	}
	return &config, nil
}

func (c *ConfigFile) Step(name string) (StepConfig, bool) {
	if c == nil {
		return StepConfig{}, false
	}
	for _, step := range c.Steps {
		if step.Name == name {
			return step, true
		}
	}
	return StepConfig{}, false
}
