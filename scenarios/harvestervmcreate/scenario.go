package harvestervmcreate

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/harvester/benchctl/scenarios/api"
	"github.com/harvester/benchctl/scenarios/harvestervm"
	"k8s.io/perf-tests/clusterloader2/pkg/measurement"
)

const name = "harvester-vm-create"

type Scenario struct{}

type runReport struct {
	Name              string
	Namespace         string
	RunID             string
	Image             string
	ImageMode         string
	CPU               harvestervm.CPUReport
	Memory            string
	DiskBus           string
	RunStrategy       string
	RequestedVMs      int
	RunningVMs        int
	StartedAt         time.Time
	FinishedAt        time.Time
	Steps             []stepReport
	APICreateDuration harvestervm.DurationStats
	RunningDuration   harvestervm.DurationStats
	VMs               []vmReport
}

type stepReport struct {
	Name       string
	Status     string
	Message    string
	StartedAt  time.Time
	FinishedAt time.Time
	DurationMS int64
}

type vmReport struct {
	Name                string
	Phase               string
	APICreateDurationMS int64
	RunningDurationMS   int64
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
	steps := make([]stepReport, 0, 4)
	var results map[string]*harvestervm.Result

	for _, stepName := range config.Steps {
		switch stepName {
		case "prepare-image":
			fmt.Printf("Preparing VM image source %s...\n", config.Image)
			if err := runStep(&steps, stepName, func() (string, error) {
				return harvestervm.PrepareImage(config)
			}); err != nil {
				return nil, err
			}
		case "create-vms":
			fmt.Printf("Creating %d KubeVirt VMs in namespace %s with image %s...\n", config.Count, config.Namespace, config.Image)
			if err := runStep(&steps, stepName, func() (string, error) {
				var err error
				results, err = harvestervm.CreateVMs(context.TODO(), client, config, runID, 0, config.Count)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Created %d VirtualMachine objects", len(results)), nil
			}); err != nil {
				return nil, err
			}
		case "wait-running":
			if results == nil {
				return nil, fmt.Errorf("wait-running requires create-vms to run first")
			}
			fmt.Printf("Waiting up to %s for VMIs to become Running...\n", config.Timeout)
			if err := runStep(&steps, stepName, func() (string, error) {
				if err := harvestervm.WaitForVMIsRunning(context.TODO(), client, config.Namespace, runID, results, config.Timeout, config.PollInterval); err != nil {
					return "", err
				}
				return fmt.Sprintf("%d/%d VMIs reached Running", harvestervm.CountRunning(results), len(results)), nil
			}); err != nil {
				return nil, err
			}
		case "cleanup-vms":
			if results == nil {
				return nil, fmt.Errorf("cleanup-vms requires create-vms to run first")
			}
			if config.Cleanup {
				fmt.Println("Cleaning up created VMs...")
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
			return nil, fmt.Errorf("unknown harvester-vm-create step %q", stepName)
		}
	}

	if results == nil {
		results = map[string]*harvestervm.Result{}
	}

	finishedAt := time.Now().UTC()
	report := buildReport(config, runID, startedAt, finishedAt, steps, results)
	fmt.Printf("VM create scenario completed: %d/%d VMIs Running\n", report.RunningVMs, report.RequestedVMs)
	return []measurement.Summary{
		&summary{report: report, format: "txt"},
		&summary{report: report, format: "json"},
	}, nil
}

func runStep(steps *[]stepReport, name string, fn func() (string, error)) error {
	step := stepReport{
		Name:      name,
		Status:    "Running",
		StartedAt: time.Now().UTC(),
	}

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
	return stepReport{
		Name:       name,
		Status:     "Skipped",
		Message:    message,
		StartedAt:  now,
		FinishedAt: now,
	}
}

func buildReport(config harvestervm.Config, runID string, startedAt, finishedAt time.Time, steps []stepReport, results map[string]*harvestervm.Result) runReport {
	vmReports := make([]vmReport, 0, len(results))
	apiDurations := make([]time.Duration, 0, len(results))
	runningDurations := make([]time.Duration, 0, len(results))

	for _, result := range results {
		apiDurations = append(apiDurations, result.APICreateDuration)
		if !result.RunningAt.IsZero() {
			runningDurations = append(runningDurations, result.RunningDuration)
		}
		vmReports = append(vmReports, vmReport{
			Name:                result.Name,
			Phase:               result.Phase,
			APICreateDurationMS: result.APICreateDuration.Milliseconds(),
			RunningDurationMS:   result.RunningDuration.Milliseconds(),
		})
	}

	sort.Slice(vmReports, func(i, j int) bool {
		return vmReports[i].Name < vmReports[j].Name
	})

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
		Memory:            config.Memory,
		DiskBus:           config.DiskBus,
		RunStrategy:       config.RunStrategy,
		RequestedVMs:      config.Count,
		RunningVMs:        len(runningDurations),
		StartedAt:         startedAt,
		FinishedAt:        finishedAt,
		Steps:             steps,
		APICreateDuration: harvestervm.DurationStatsFrom(apiDurations),
		RunningDuration:   harvestervm.DurationStatsFrom(runningDurations),
		VMs:               vmReports,
	}
}

func (s *summary) SummaryName() string {
	return "HarvesterVMCreate"
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
	fmt.Fprintf(&b, "Harvester VM Create Summary\n")
	fmt.Fprintf(&b, "===========================\n")
	fmt.Fprintf(&b, "Run ID: %s\n", s.report.RunID)
	fmt.Fprintf(&b, "Namespace: %s\n", s.report.Namespace)
	fmt.Fprintf(&b, "Image: %s\n", s.report.Image)
	fmt.Fprintf(&b, "Image Mode: %s\n", s.report.ImageMode)
	fmt.Fprintf(&b, "CPU: cores=%d sockets=%d threads=%d maxSockets=%d\n",
		s.report.CPU.Cores,
		s.report.CPU.Sockets,
		s.report.CPU.Threads,
		s.report.CPU.MaxSockets,
	)
	fmt.Fprintf(&b, "Memory: %s\n", s.report.Memory)
	fmt.Fprintf(&b, "Disk Bus: %s\n", s.report.DiskBus)
	fmt.Fprintf(&b, "Run Strategy: %s\n", s.report.RunStrategy)
	fmt.Fprintf(&b, "Started: %s\n", s.report.StartedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "Finished: %s\n", s.report.FinishedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "VMIs Running: %d/%d\n\n", s.report.RunningVMs, s.report.RequestedVMs)

	fmt.Fprintf(&b, "Steps:\n")
	for _, step := range s.report.Steps {
		fmt.Fprintf(&b, "- %s: %s (%dms) %s\n", step.Name, step.Status, step.DurationMS, step.Message)
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "API Create Duration: avg %dms, p50 %dms, p95 %dms, p99 %dms, max %dms\n",
		s.report.APICreateDuration.AvgMS,
		s.report.APICreateDuration.P50MS,
		s.report.APICreateDuration.P95MS,
		s.report.APICreateDuration.P99MS,
		s.report.APICreateDuration.MaxMS,
	)
	fmt.Fprintf(&b, "Running Duration: avg %dms, p50 %dms, p95 %dms, p99 %dms, max %dms\n",
		s.report.RunningDuration.AvgMS,
		s.report.RunningDuration.P50MS,
		s.report.RunningDuration.P95MS,
		s.report.RunningDuration.P99MS,
		s.report.RunningDuration.MaxMS,
	)
	return b.String()
}
