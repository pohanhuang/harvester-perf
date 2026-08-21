package harvestervmcapacity

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/harvester/benchctl/scenarios/api"
	"github.com/harvester/benchctl/scenarios/harvestervm"
	"k8s.io/client-go/dynamic"
	"k8s.io/perf-tests/clusterloader2/pkg/measurement"
)

const name = "harvester-vm-capacity"

type Scenario struct{}

type runReport struct {
	Name         string
	Namespace    string
	RunID        string
	Image        string
	ImageMode    string
	CPU          harvestervm.CPUReport
	Memory       string
	BatchSize    int
	MaxVMs       int
	RunningVMs   int
	RequestedVMs int
	FailedBatch  int
	Failure      string
	StartedAt    time.Time
	FinishedAt   time.Time
	Steps        []stepReport
	Batches      []batchReport
}

type stepReport struct {
	Name       string
	Status     string
	Message    string
	StartedAt  time.Time
	FinishedAt time.Time
	DurationMS int64
}

type batchReport struct {
	Batch             int
	Created           int
	TotalRequested    int
	TotalRunning      int
	StartedAt         time.Time
	FinishedAt        time.Time
	DurationMS        int64
	RunningDuration   harvestervm.DurationStats
	APICreateDuration harvestervm.DurationStats
}

type summary struct {
	report runReport
	format string
}

func New() *Scenario {
	return &Scenario{}
}

func (s *Scenario) Name() string {
	return name
}

func (s *Scenario) Run(ctx api.Context) ([]measurement.Summary, error) {
	config := configFromContext(ctx)
	if ctx.Framework == nil {
		return nil, fmt.Errorf("framework is required")
	}

	client := ctx.Framework.GetDynamicClients().GetClient()
	runID := fmt.Sprintf("%s-%d", config.NamePrefix, time.Now().UTC().Unix())
	startedAt := time.Now().UTC()
	steps := make([]stepReport, 0, len(config.Steps))
	results := map[string]*harvestervm.Result{}
	batches := []batchReport{}
	failure := ""
	failedBatch := 0

	for _, stepName := range config.Steps {
		switch stepName {
		case "prepare-image":
			fmt.Printf("Preparing VM image source %s...\n", config.Image)
			if err := runStep(&steps, stepName, func() (string, error) {
				return harvestervm.PrepareImage(config)
			}); err != nil {
				return nil, err
			}
		case "create-batches":
			fmt.Printf("Creating VMs in batches of %d up to %d total VMs...\n", config.BatchSize, config.MaxVMs)
			if err := runStep(&steps, stepName, func() (string, error) {
				var err error
				batches, failedBatch, failure, err = runCapacityBatches(context.TODO(), client, config, runID, results)
				if err != nil {
					return "", err
				}
				if failure != "" {
					return fmt.Sprintf("Stopped at %d/%d Running: %s", harvestervm.CountRunning(results), len(results), failure), nil
				}
				return fmt.Sprintf("Reached configured maxVMs with %d/%d Running", harvestervm.CountRunning(results), len(results)), nil
			}); err != nil {
				return nil, err
			}
		case "cleanup-vms":
			if config.Cleanup {
				fmt.Println("Cleaning up capacity test VMs...")
				if err := runStep(&steps, stepName, func() (string, error) {
					if err := harvestervm.CleanupVMs(context.TODO(), client, config.Namespace, runID); err != nil {
						return "", fmt.Errorf("cleanup VMs: %w", err)
					}
					return "Deleted created VirtualMachine objects", nil
				}); err != nil {
					return nil, err
				}
			} else {
				steps = append(steps, skippedStep(stepName, "Cleanup disabled; created VMs are left in the cluster"))
			}
		default:
			return nil, fmt.Errorf("unknown harvester-vm-capacity step %q", stepName)
		}
	}

	report := buildReport(config, runID, startedAt, time.Now().UTC(), steps, batches, results, failedBatch, failure)
	fmt.Printf("VM capacity scenario completed: %d/%d VMIs Running\n", report.RunningVMs, report.RequestedVMs)
	return []measurement.Summary{
		&summary{report: report, format: "txt"},
		&summary{report: report, format: "json"},
	}, nil
}

func runCapacityBatches(ctx context.Context, client dynamic.Interface, config harvestervm.Config, runID string, results map[string]*harvestervm.Result) ([]batchReport, int, string, error) {
	batches := []batchReport{}
	for requested := 0; requested < config.MaxVMs; {
		batchNumber := len(batches) + 1
		batchSize := config.BatchSize
		if requested+batchSize > config.MaxVMs {
			batchSize = config.MaxVMs - requested
		}

		startedAt := time.Now().UTC()
		created, err := harvestervm.CreateVMs(ctx, client, config, runID, requested, batchSize)
		if err != nil {
			return batches, batchNumber, err.Error(), nil
		}
		harvestervm.MergeResults(results, created)
		requested += batchSize

		if err := harvestervm.WaitForVMIsRunning(ctx, client, config.Namespace, runID, results, config.Timeout, config.PollInterval); err != nil {
			return batches, batchNumber, err.Error(), err
		}

		finishedAt := time.Now().UTC()
		report := buildBatchReport(batchNumber, batchSize, requested, startedAt, finishedAt, created, results)
		batches = append(batches, report)
		if report.TotalRunning < report.TotalRequested {
			return batches, batchNumber, fmt.Sprintf("timeout waiting for batch %d: %d/%d Running", batchNumber, report.TotalRunning, report.TotalRequested), nil
		}
	}
	return batches, 0, "", nil
}

func buildBatchReport(batchNumber, created, totalRequested int, startedAt, finishedAt time.Time, createdResults, allResults map[string]*harvestervm.Result) batchReport {
	apiDurations := make([]time.Duration, 0, len(createdResults))
	runningDurations := make([]time.Duration, 0, len(createdResults))
	for _, result := range createdResults {
		apiDurations = append(apiDurations, result.APICreateDuration)
		if !result.RunningAt.IsZero() {
			runningDurations = append(runningDurations, result.RunningDuration)
		}
	}

	return batchReport{
		Batch:             batchNumber,
		Created:           created,
		TotalRequested:    totalRequested,
		TotalRunning:      harvestervm.CountRunning(allResults),
		StartedAt:         startedAt,
		FinishedAt:        finishedAt,
		DurationMS:        finishedAt.Sub(startedAt).Milliseconds(),
		RunningDuration:   harvestervm.DurationStatsFrom(runningDurations),
		APICreateDuration: harvestervm.DurationStatsFrom(apiDurations),
	}
}

func runStep(steps *[]stepReport, name string, fn func() (string, error)) error {
	step := stepReport{Name: name, Status: "Running", StartedAt: time.Now().UTC()}
	message, err := fn()
	step.FinishedAt = time.Now().UTC()
	step.DurationMS = step.FinishedAt.Sub(step.StartedAt).Milliseconds()
	step.Message = message
	if err != nil {
		step.Status = "Failed"
		step.Message = err.Error()
		*steps = append(*steps, step)
		return fmt.Errorf("%s: %w", name, err)
	}
	step.Status = "Succeeded"
	*steps = append(*steps, step)
	return nil
}

func skippedStep(name, message string) stepReport {
	now := time.Now().UTC()
	return stepReport{Name: name, Status: "Skipped", Message: message, StartedAt: now, FinishedAt: now}
}

func buildReport(config harvestervm.Config, runID string, startedAt, finishedAt time.Time, steps []stepReport, batches []batchReport, results map[string]*harvestervm.Result, failedBatch int, failure string) runReport {
	return runReport{
		Name:      name,
		Namespace: config.Namespace,
		RunID:     runID,
		Image:     config.Image,
		ImageMode: config.ImageMode,
		CPU: harvestervm.CPUReport{
			Cores:      config.CPUCores,
			Sockets:    config.CPUSockets,
			Threads:    config.CPUThreads,
			MaxSockets: config.CPUMaxSockets,
		},
		Memory:       config.Memory,
		BatchSize:    config.BatchSize,
		MaxVMs:       config.MaxVMs,
		RequestedVMs: len(results),
		RunningVMs:   harvestervm.CountRunning(results),
		FailedBatch:  failedBatch,
		Failure:      failure,
		StartedAt:    startedAt,
		FinishedAt:   finishedAt,
		Steps:        steps,
		Batches:      batches,
	}
}

func (s *summary) SummaryName() string {
	return "HarvesterVMCapacity"
}

func (s *summary) SummaryExt() string {
	return s.format
}

func (s *summary) SummaryTime() time.Time {
	return s.report.StartedAt
}

func (s *summary) SummaryContent() string {
	if s.format == "json" {
		data, err := json.MarshalIndent(s.report, "", "  ")
		if err != nil {
			return fmt.Sprintf("{\"error\": %q}", err.Error())
		}
		return string(data)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Harvester VM Capacity Summary\n")
	fmt.Fprintf(&b, "=============================\n")
	fmt.Fprintf(&b, "Run ID: %s\n", s.report.RunID)
	fmt.Fprintf(&b, "Namespace: %s\n", s.report.Namespace)
	fmt.Fprintf(&b, "Image: %s\n", s.report.Image)
	fmt.Fprintf(&b, "VM Spec: cpu=%d memory=%s\n", s.report.CPU.Cores*s.report.CPU.Sockets*s.report.CPU.Threads, s.report.Memory)
	fmt.Fprintf(&b, "Batch Size: %d\n", s.report.BatchSize)
	fmt.Fprintf(&b, "Max VMs: %d\n", s.report.MaxVMs)
	fmt.Fprintf(&b, "Running VMs: %d/%d\n", s.report.RunningVMs, s.report.RequestedVMs)
	if s.report.Failure != "" {
		fmt.Fprintf(&b, "Failure: batch %d: %s\n", s.report.FailedBatch, s.report.Failure)
	}
	fmt.Fprintf(&b, "\nBatches:\n")
	for _, batch := range s.report.Batches {
		fmt.Fprintf(&b, "- batch %d: total %d/%d Running, duration %dms, running avg %dms p95 %dms\n",
			batch.Batch,
			batch.TotalRunning,
			batch.TotalRequested,
			batch.DurationMS,
			batch.RunningDuration.AvgMS,
			batch.RunningDuration.P95MS,
		)
	}
	return b.String()
}
