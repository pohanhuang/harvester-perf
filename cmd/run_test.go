package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/harvester/hvperf/internal/suites/density"
	pkgoptions "github.com/harvester/hvperf/internal/suites/options"
	"github.com/harvester/hvperf/pkg/suites"
)

func TestLoadRunOptionsParsesDensity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hvperf.yaml")
	yaml := "density:\n  batchWaitTimeout: 90s\n  vmi:\n    cpu: 200m\n    memory: 90Mi\nother-suite:\n  nested:\n    enabled: true\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	opts, err := loadRunOptions(path)
	if err != nil {
		t.Fatal(err)
	}
	section, ok := opts["density"]
	if !ok {
		t.Fatal("density key missing")
	}
	m, ok := section.(suites.Options)
	if !ok {
		t.Fatalf("density section type = %T", section)
	}
	vmi, _ := m["vmi"].(suites.Options)
	if m["batchWaitTimeout"] != "90s" || vmi["cpu"] != "200m" || vmi["memory"] != "90Mi" {
		t.Fatalf("density options = %#v", m)
	}
	decoded, err := pkgoptions.DecodeSection[density.Options](opts, "density")
	if err != nil || decoded.BatchWaitTimeout.String() != "1m30s" || decoded.VMI.CPU != "200m" {
		t.Fatalf("decoded density = %+v, err = %v", decoded, err)
	}
	other, err := pkgoptions.DecodeSection[suites.Options](opts, "other-suite")
	if err != nil {
		t.Fatal(err)
	}
	if other["nested"].(suites.Options)["enabled"] != true {
		t.Fatalf("additional suite = %#v", other)
	}
	global, err := pkgoptions.FromOptions[struct{ EtcdNamespace string }](&opts)
	if err != nil || global.EtcdNamespace != "kube-system" {
		t.Fatalf("global options = %+v, err = %v", global, err)
	}
	for key, want := range *suites.DefaultGlobalOptions() {
		if opts[key] != want {
			t.Fatalf("global option %s changed: %v", key, opts[key])
		}
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
