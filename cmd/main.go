package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/harvester/benchctl/cl2"
	"github.com/harvester/benchctl/scenarios"
	scenarioapi "github.com/harvester/benchctl/scenarios/api"
	"k8s.io/perf-tests/clusterloader2/pkg/config"
	"k8s.io/perf-tests/clusterloader2/pkg/framework"
	"k8s.io/perf-tests/clusterloader2/pkg/measurement"
	"k8s.io/perf-tests/clusterloader2/pkg/provider"

	// Import ClusterLoader2 built-in measurements
	_ "k8s.io/perf-tests/clusterloader2/pkg/measurement/common"

	// Import our custom measurements
	_ "github.com/harvester/benchctl/measurements"
)

func main() {
	kubeconfig := flag.String("kubeconfig", "", "Path to kubeconfig file (empty for in-cluster config)")
	testName := flag.String("test", "resource-usage", "Test to run (hello, resource-usage, platform-baseline)")
	outputDir := flag.String("output-dir", "/tmp/benchctl-results", "Directory to write summary artifacts")
	platformBaselineDuration := flag.Duration("platform-baseline-duration", 30*time.Second, "How long to sample for platform-baseline")
	platformBaselineInterval := flag.Duration("platform-baseline-interval", 10*time.Second, "Sampling interval for platform-baseline")
	vmCreateCount := flag.Int("vm-create-count", 50, "Number of VMs to create for harvester-vm-create")
	vmCreateNamespace := flag.String("vm-create-namespace", "default", "Namespace for harvester-vm-create VMs")
	vmCreateImage := flag.String("vm-create-image", "quay.io/kubevirt/cirros-container-disk-demo:latest", "ContainerDisk image for harvester-vm-create VMs")
	vmCreateTimeout := flag.Duration("vm-create-timeout", 20*time.Minute, "Timeout waiting for created VMIs to become Running")
	vmCreateCleanup := flag.Bool("vm-create-cleanup", true, "Delete VMs created by harvester-vm-create after collecting results")
	scenarioConfigPath := flag.String("scenario-config", "", "Path to a BenchCTL scenario YAML config")
	cl2ConfigPath := flag.String("cl2-config", "", "Path to a ClusterLoader2 config YAML to run through the CL2 adapter")
	cl2Identifier := flag.String("cl2-identifier", "", "Optional identifier for the CL2 test scenario")
	cl2OverridePaths := flag.String("cl2-overrides", "", "Comma-separated ClusterLoader2 override YAML paths")
	flag.Parse()

	fmt.Println("🚀 BenchCTL - Harvester Benchmark Tool")
	fmt.Println("========================================")

	var scenarioConfig *scenarioapi.ConfigFile
	if *scenarioConfigPath != "" {
		loadedConfig, loadErr := scenarioapi.LoadConfigFile(*scenarioConfigPath)
		if loadErr != nil {
			fmt.Printf("❌ Loading scenario config failed: %v\n", loadErr)
			os.Exit(1)
		}
		scenarioConfig = loadedConfig
		if scenarioConfig.Name != "" {
			*testName = scenarioConfig.Name
		}
	} else if hasBuiltinScenarioConfig(*testName) {
		loadedConfig, loadErr := scenarioapi.LoadBuiltinConfig(*testName)
		if loadErr != nil {
			fmt.Printf("❌ Loading built-in scenario config failed: %v\n", loadErr)
			os.Exit(1)
		}
		scenarioConfig = loadedConfig
	}

	// 1. Create ClusterLoader2 framework
	if *kubeconfig == "" {
		fmt.Println("📦 Using in-cluster configuration")
	} else {
		fmt.Printf("📦 Initializing framework with kubeconfig: %s\n", *kubeconfig)
	}

	clusterProvider, err := provider.NewProvider(&provider.InitOptions{
		ProviderName: provider.LocalName,
	})
	if err != nil {
		fmt.Printf("❌ Provider initialization failed: %v\n", err)
		os.Exit(1)
	}

	clusterConfig := &config.ClusterConfig{
		KubeConfigPath:   *kubeconfig, // Empty string triggers in-cluster config
		RunFromCluster:   *kubeconfig == "",
		Provider:         clusterProvider,
		K8SClientsNumber: 5,
		KubeletPort:      10250,
	}

	f, err := framework.NewFramework(clusterConfig, clusterConfig.K8SClientsNumber)
	if err != nil {
		fmt.Printf("❌ Framework creation failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✅ Framework initialized")

	// 2. Create measurement manager
	fmt.Println("📊 Creating measurement manager...")
	clusterLoaderConfig := &config.ClusterLoaderConfig{
		ClusterConfig: *clusterConfig,
	}
	mgr := measurement.CreateManager(f, nil, nil, clusterLoaderConfig)
	fmt.Println("✅ Manager created")

	// 3. Run the selected test
	if *cl2ConfigPath != "" {
		fmt.Printf("\n🧪 Running CL2 config: %s\n", *cl2ConfigPath)
	} else {
		fmt.Printf("\n🧪 Running test: %s\n", *testName)
	}
	fmt.Println("========================================")

	var scenarioSummaries []measurement.Summary
	if *cl2ConfigPath != "" {
		if err := cl2.RunConfig(cl2.RunOptions{
			Framework:     f,
			ClusterConfig: clusterLoaderConfig,
			ConfigPath:    *cl2ConfigPath,
			Identifier:    *cl2Identifier,
			OverridePaths: splitComma(*cl2OverridePaths),
			ReportDir:     *outputDir,
		}); err != nil {
			fmt.Printf("❌ Error running CL2 config: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✅ CL2 config completed")
	} else {
		var err error
		scenarioSummaries, err = scenarios.Run(*testName, scenarioapi.Context{
			Framework:                f,
			Measurements:             mgr,
			Config:                   scenarioConfig,
			PlatformBaselineDuration: *platformBaselineDuration,
			PlatformBaselineInterval: *platformBaselineInterval,
			VMCreateCount:            *vmCreateCount,
			VMCreateNamespace:        *vmCreateNamespace,
			VMCreateImage:            *vmCreateImage,
			VMCreateTimeout:          *vmCreateTimeout,
			VMCreateCleanup:          *vmCreateCleanup,
		})
		if err != nil {
			fmt.Printf("❌ Error running scenario: %v\n", err)
			fmt.Printf("Available scenarios: %s\n", strings.Join(scenarios.Names(), ", "))
			os.Exit(1)
		}
	}

	// 4. Print results
	fmt.Println("\n📋 Results:")
	fmt.Println("========================================")
	if *cl2ConfigPath != "" {
		fmt.Printf("CL2 reports written to %s\n", *outputDir)
		mgr.Dispose()
		fmt.Println("\n✅ Test completed successfully!")
		return
	}

	summaries := append(mgr.GetSummaries(), scenarioSummaries...)
	if len(summaries) == 0 {
		fmt.Println("No summaries collected")
	}
	for _, summary := range summaries {
		fmt.Printf("\n--- %s ---\n", summary.SummaryName())
		fmt.Println(summary.SummaryContent())
	}
	if err := writeSummaries(summaries, *outputDir); err != nil {
		fmt.Printf("❌ Writing summary files failed: %v\n", err)
	} else if len(summaries) > 0 {
		fmt.Printf("\n📁 Summary files written to %s\n", *outputDir)
	}

	// 5. Cleanup
	mgr.Dispose()
	fmt.Println("\n✅ Test completed successfully!")
}

func hasBuiltinScenarioConfig(name string) bool {
	switch name {
	case "harvester-vm-create", "harvester-vm-capacity":
		return true
	default:
		return false
	}
}

func writeSummaries(summaries []measurement.Summary, outputDir string) error {
	if outputDir == "" {
		return nil
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return err
	}

	for i, summary := range summaries {
		filename := fmt.Sprintf("%02d-%s.%s", i+1, sanitizeFilename(summary.SummaryName()), summary.SummaryExt())
		path := filepath.Join(outputDir, filename)
		if err := os.WriteFile(path, []byte(summary.SummaryContent()), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func sanitizeFilename(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ReplaceAll(name, "/", "-")
	return name
}

func splitComma(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
