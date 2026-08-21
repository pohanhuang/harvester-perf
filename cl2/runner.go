package cl2

import (
	"fmt"
	"os"
	"path/filepath"

	cl2config "k8s.io/perf-tests/clusterloader2/pkg/config"
	"k8s.io/perf-tests/clusterloader2/pkg/framework"
	"k8s.io/perf-tests/clusterloader2/pkg/test"
)

type RunOptions struct {
	Framework           *framework.Framework
	PrometheusFramework *framework.Framework
	ClusterConfig       *cl2config.ClusterLoaderConfig
	ConfigPath          string
	Identifier          string
	OverridePaths       []string
	ReportDir           string
}

func CompileConfig(opts RunOptions) (*Config, error) {
	ctx, err := createContext(opts)
	if err != nil {
		return nil, err
	}

	config, errList := test.CompileTestConfig(ctx)
	if !errList.IsEmpty() {
		return config, fmt.Errorf("compiling CL2 config: %s", errList.String())
	}
	return config, nil
}

func RunConfig(opts RunOptions) error {
	ctx, err := createContext(opts)
	if err != nil {
		return err
	}

	config, errList := test.CompileTestConfig(ctx)
	if !errList.IsEmpty() {
		return fmt.Errorf("compiling CL2 config: %s", errList.String())
	}
	ctx.SetTestConfig(config)

	reporter := ctx.GetTestReporter()
	reporter.BeginTestSuite()
	errList = test.RunTest(ctx)
	reporter.EndTestSuite()
	if !errList.IsEmpty() {
		return fmt.Errorf("running CL2 config: %s", errList.String())
	}
	return nil
}

func createContext(opts RunOptions) (test.Context, error) {
	if opts.Framework == nil {
		return nil, fmt.Errorf("framework is required")
	}
	if opts.ClusterConfig == nil {
		return nil, fmt.Errorf("cluster config is required")
	}
	if opts.ConfigPath == "" {
		return nil, fmt.Errorf("config path is required")
	}

	clusterConfig := *opts.ClusterConfig
	if opts.ReportDir != "" {
		if err := os.MkdirAll(opts.ReportDir, 0o755); err != nil {
			return nil, fmt.Errorf("creating report dir: %w", err)
		}
		clusterConfig.ReportDir = opts.ReportDir
	}

	scenario := &TestScenario{
		Identifier:    opts.Identifier,
		ConfigPath:    opts.ConfigPath,
		OverridePaths: opts.OverridePaths,
	}
	reporter := test.CreateSimpleReporter(filepath.Join(clusterConfig.ReportDir, "junit.xml"), "BenchCTL CL2 Adapter")

	ctx, errList := test.CreateTestContext(
		opts.Framework,
		opts.PrometheusFramework,
		&clusterConfig,
		reporter,
		scenario,
	)
	if !errList.IsEmpty() {
		return nil, fmt.Errorf("creating CL2 test context: %s", errList.String())
	}
	return ctx, nil
}
