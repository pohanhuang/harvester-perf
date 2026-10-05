package suites

import "time"

const DefaultNamespace = "harvester-perf-system"

// Options contains custom options for test suites. The keys are the names of the
// test suites, and the values are the options for each suite.
type Options map[string]any

// DefaultGlobalOptions returns the default options for the system test suite.
func DefaultGlobalOptions() *Options {
	return &Options{
		"EtcdNamespace":                   "kube-system",
		"EtcdReadyTimeout":                300 * time.Second,
		"JobActiveDeadline":               3600 * time.Second,
		"JobPodContainerName":             "benchmark",
		"JobPodImageName":                 "registry.suse.com/bci/bci-base",
		"JobPodImageTag":                  "latest",
		"JobPodTTLAfterFinished":          300 * time.Second,
		"JobPodReadyTimeout":              3600 * time.Second,
		"JobSuspend":                      false,
		"MonitoringAddonName":             "rancher-monitoring",
		"MonitoringNamespace":             "cattle-monitoring-system",
		"MonitoringRangeDuration":         300 * time.Second,
		"MonitoringWaitPodMonitorTimeout": 600 * time.Second, // 2x the EtcdRangeDuration to allow for Prometheus to scrape etcd metrics
		"NamespaceReadyTimeout":           300 * time.Second, // accommodate for cleanup in back-to-back test runs
		"PodActiveDeadline":               3600 * time.Second,
		"PodImageName":                    "registry.suse.com/bci/bci-base",
		"PodImageTag":                     "latest",
		"PodReadyTimeout":                 3600 * time.Second,
	}
}
