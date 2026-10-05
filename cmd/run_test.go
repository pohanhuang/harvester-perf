package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/harvester/hvperf/pkg/suites"
)

func TestLoadRunOptionsOverridesGlobalsAndPreservesSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hvperf.yaml")
	config := `
EtcdNamespace: custom-etcd
example-suite:
  enabled: true
  workload:
    name: sample
`
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	opts, err := loadRunOptions(path)
	if err != nil {
		t.Fatal(err)
	}

	want := *suites.DefaultGlobalOptions()
	want["EtcdNamespace"] = "custom-etcd"
	want["example-suite"] = suites.Options{
		"enabled": true,
		"workload": suites.Options{
			"name": "sample",
		},
	}
	if !reflect.DeepEqual(opts, want) {
		t.Fatalf("options:\n got  %#v\n want %#v", opts, want)
	}
}

func TestLoadRunOptionsOptionalConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	opts, err := loadRunOptions("")
	if err != nil || !reflect.DeepEqual(opts, *suites.DefaultGlobalOptions()) {
		t.Fatalf("default options = %#v, err = %v", opts, err)
	}
	if _, err := loadRunOptions("./hvperf.yaml"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("explicit missing config error = %v", err)
	}
	if got := runCmd.PersistentFlags().Lookup("config").DefValue; got != "" {
		t.Fatalf("config flag default = %q", got)
	}
}

func TestLoadRunOptionsMissingCustomPath(t *testing.T) {
	if _, err := loadRunOptions(filepath.Join(t.TempDir(), "missing.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing custom config error = %v", err)
	}
}
