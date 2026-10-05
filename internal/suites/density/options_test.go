package density

import (
	"reflect"
	"testing"
	"time"

	pkgoptions "github.com/harvester/hvperf/internal/suites/options"
	pkgsuites "github.com/harvester/hvperf/pkg/suites"
)

func TestDecodeOptionsUsesDefaultsWhenSectionMissing(t *testing.T) {
	got, err := pkgoptions.DecodeSection(pkgsuites.Options{}, "density", DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if want := DefaultOptions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("options:\n got  %+v\n want %+v", got, want)
	}
}

func TestDecodeOptionsOverridesProvidedFields(t *testing.T) {
	opts := pkgsuites.Options{
		"density": pkgsuites.Options{
			"batchWaitTimeout": "90s",
			"vmi": pkgsuites.Options{
				"cpu": "200m",
			},
		},
	}
	got, err := pkgoptions.DecodeSection(opts, "density", DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	want := DefaultOptions()
	want.BatchWaitTimeout = 90 * time.Second
	want.VMI.CPU = "200m"
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("options:\n got  %+v\n want %+v", got, want)
	}
}
