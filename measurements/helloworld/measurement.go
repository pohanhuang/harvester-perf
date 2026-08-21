package helloworld

import (
	"fmt"
	"time"

	"k8s.io/perf-tests/clusterloader2/pkg/measurement"
)

const (
	helloWorldMeasurementName = "HelloWorld"
)

func init() {
	// Register our custom measurement with ClusterLoader2
	if err := measurement.Register(helloWorldMeasurementName, createHelloWorldMeasurement); err != nil {
		panic(fmt.Sprintf("Cannot register %s: %v", helloWorldMeasurementName, err))
	}
}

func createHelloWorldMeasurement() measurement.Measurement {
	return &helloWorldMeasurement{}
}

// helloWorldMeasurement is a simple custom measurement that demonstrates
// how to implement the Measurement interface
type helloWorldMeasurement struct {
	startTime time.Time
	message   string
	counter   int
}

// Execute is called by the measurement manager
// It follows the start/gather pattern used by ClusterLoader2
func (h *helloWorldMeasurement) Execute(config *measurement.Config) ([]measurement.Summary, error) {
	// Get the action parameter (start or gather)
	action, ok := config.Params["action"].(string)
	if !ok {
		return nil, fmt.Errorf("action parameter is required")
	}

	switch action {
	case "start":
		return h.start(config)
	case "gather":
		return h.gather(config)
	default:
		return nil, fmt.Errorf("unknown action: %s", action)
	}
}

func (h *helloWorldMeasurement) start(config *measurement.Config) ([]measurement.Summary, error) {
	h.startTime = time.Now()
	h.counter = 0

	// Get optional message parameter
	if msg, ok := config.Params["message"].(string); ok {
		h.message = msg
	} else {
		h.message = defaultMessage
	}

	fmt.Printf("🎯 HelloWorld measurement started at %s\n", h.startTime.Format(time.RFC3339))
	fmt.Printf("💬 Message: %s\n", h.message)

	// Simulate some initialization work
	h.counter++

	return nil, nil
}

func (h *helloWorldMeasurement) gather(config *measurement.Config) ([]measurement.Summary, error) {
	duration := time.Since(h.startTime)

	fmt.Printf("🏁 HelloWorld measurement completed\n")
	fmt.Printf("⏱️  Duration: %v\n", duration)

	// Create a summary
	summary := &helloSummary{
		message:   h.message,
		duration:  duration,
		counter:   h.counter,
		startTime: h.startTime,
	}

	return []measurement.Summary{summary}, nil
}

// Dispose cleans up resources (called when measurement is done)
func (h *helloWorldMeasurement) Dispose() {
	// Nothing to cleanup in this simple example
	fmt.Println("🧹 HelloWorld measurement disposed")
}

// String returns the measurement name
func (h *helloWorldMeasurement) String() string {
	return helloWorldMeasurementName
}

// helloSummary implements the Summary interface
type helloSummary struct {
	message   string
	duration  time.Duration
	counter   int
	startTime time.Time
}

func (s *helloSummary) SummaryName() string {
	return helloWorldMeasurementName
}

func (s *helloSummary) SummaryExt() string {
	return "txt"
}

func (s *helloSummary) SummaryTime() time.Time {
	return s.startTime
}

func (s *helloSummary) SummaryContent() string {
	return fmt.Sprintf(`
HelloWorld Measurement Summary
==============================
Message:    %s
Duration:   %v
Counter:    %d
Start Time: %s
End Time:   %s

This is a custom measurement that demonstrates:
- How to implement the Measurement interface
- How to use the start/gather pattern
- How to return a Summary
- How to integrate with ClusterLoader2 framework
`,
		s.message,
		s.duration,
		s.counter,
		s.startTime.Format(time.RFC3339),
		s.startTime.Add(s.duration).Format(time.RFC3339),
	)
}
