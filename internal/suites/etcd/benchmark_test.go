package etcd

import (
	"reflect"
	"testing"

	"github.com/harvester/hvperf/internal/suites/options"
	pkgsuites "github.com/harvester/hvperf/pkg/suites"
)

func TestBenchmarkOptionsDefaults(t *testing.T) {
	sysOpts, err := options.FromOptions[*BenchmarkOptions](pkgsuites.DefaultGlobalOptions())
	if err != nil {
		t.Fatalf("Failed to convert default global options to BenchmarkOptions: %v", err)
	}

	expected := &BenchmarkOptions{
		EtcdBenchmarkLocalPath: "/usr/local/bin/benchmark",
		EtcdctlLocalPath:       "/usr/local/bin/etcdctl",
		PromtoolLocalPath:      "/usr/local/bin/promtool",

		EtcdNamespace:           sysOpts.EtcdNamespace,
		EtcdctlOutputFormat:     "simple",
		EtcdEndpoints:           "https://127.0.0.1:2379",
		EtcdMetricsPath:         "/metrics",
		EtcdMetricsPortName:     "metrics",
		EtcdMetricsScheme:       "http",
		EtcdReadyTimeout:        sysOpts.EtcdReadyTimeout,
		EtcdRemoteCopyTargetDir: "/usr/local/bin/",
		EtcdRemoteTLSCertDir:    "/host/rancher/rke2/server/tls/etcd",

		JobActiveDeadline:      sysOpts.JobActiveDeadline,
		JobPodContainerName:    sysOpts.JobPodContainerName,
		JobPodImageName:        sysOpts.JobPodImageName,
		JobPodImageTag:         sysOpts.JobPodImageTag,
		JobPodNode:             sysOpts.JobPodNode,
		JobPodTTLAfterFinished: sysOpts.JobPodTTLAfterFinished,
		JobPodReadyTimeout:     sysOpts.JobPodReadyTimeout,
		JobSuspend:             sysOpts.JobSuspend,

		MonitoringAddonName:             sysOpts.MonitoringAddonName,
		MonitoringNamespace:             sysOpts.MonitoringNamespace,
		MonitoringOutputFormat:          "promql",
		MonitoringRangeDuration:         sysOpts.MonitoringRangeDuration,
		MonitoringWaitPodMonitorTimeout: sysOpts.MonitoringWaitPodMonitorTimeout,

		NamespaceReadyTimeout: sysOpts.NamespaceReadyTimeout,

		CheckPerfLoadSize: DefaultCheckPerfLoadSize,
		PutLoadSize:       DefaultLoadSize,
		PutKeySize:        DefaultKeySize,
		PutValSize:        DefaultPutValSize,
		GRPCClientCount:   DefaultClientCount,
		GRPCConnCount:     DefaultConnCount,
	}
	actual, err := BenchmarkOptionsDefaults(*pkgsuites.DefaultGlobalOptions())
	if err != nil {
		t.Fatalf("BenchmarkOptionsDefaults() returned an error: %v", err)
	}

	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("BenchmarkOptionsDefaults() = %v, want %v", actual, expected)
	}
}
