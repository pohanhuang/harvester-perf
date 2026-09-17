package density

import (
	"context"
	"fmt"
	"time"

	"github.com/harvester/hvperf/pkg/resource"
	pkgsuites "github.com/harvester/hvperf/pkg/suites"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const vmImageURL = "https://download.cirros-cloud.net/0.6.2/cirros-0.6.2-x86_64-disk.img"

var _ pkgsuites.Suite = &DensitySuite{}

func init() {
	suite := &DensitySuite{}
	suite.Marshal = suite
	pkgsuites.Register(suite)
}

type DensitySuite struct {
	pkgsuites.SuiteMarshaler
	*pkgsuites.Clients
}

type densityOptions struct {
	ImageURL     string
	StorageClass string
	WaitTimeout  time.Duration
}

func defaultOptions() densityOptions {
	return densityOptions{
		ImageURL:     vmImageURL,
		StorageClass: "longhorn",
		WaitTimeout:  15 * time.Minute,
	}
}

func (s *DensitySuite) SetProgressReporter(_ *pkgsuites.ProgressReporter) {}
func (s *DensitySuite) Name() string                                      { return "density" }
func (s *DensitySuite) Description() string {
	return "import a VM image and create a Harvester VM"
}
func (s *DensitySuite) IsReadWrite() bool                     { return true }
func (s *DensitySuite) SetClients(clients *pkgsuites.Clients) { s.Clients = clients }

func (s *DensitySuite) RunE(ctx context.Context, runID, namespace string, _ pkgsuites.Options) (pkgsuites.SuiteResult, error) {
	o := defaultOptions()
	result := pkgsuites.SuiteResult{Name: s.Name(), RunID: runID}
	defer s.cleanup(context.Background(), namespace, runID)

	imageName := runID + "-image"
	start := time.Now()
	imageErr := resource.CreateAndWaitVMImage(ctx, s.DynClientSet, imageName, namespace, runID, o.ImageURL, o.WaitTimeout)
	result.Results = append(result.Results, caseResult("vm-image-import", start, imageErr))
	if imageErr != nil {
		return result, imageErr
	}
	return result, nil
	// vmName := runID + "-vm"
	// start = time.Now()
	// vmErr := s.createAndWaitVM(ctx, vmName, runID, namespace, imageName, o)
	// result.Results = append(result.Results, caseResult("vm-create", start, vmErr))
	// return result, vmErr
}

func (s *DensitySuite) createAndWaitVM(ctx context.Context, name, runID, namespace, imageName string, o densityOptions) error {
	imageID := fmt.Sprintf("%s/%s", namespace, imageName)
	if err := resource.CreateVM(ctx, s.DynClientSet, name, namespace, runID, imageID, o.StorageClass); err != nil {
		return err
	}
	return resource.WaitVM(ctx, s.DynClientSet, namespace, name, o.WaitTimeout)
}

func (s *DensitySuite) cleanup(ctx context.Context, namespace, runID string) {
	selector := metav1.ListOptions{LabelSelector: resource.RunLabel + "=" + runID}
	_ = s.DynClientSet.Resource(resource.VMGVR).Namespace(namespace).DeleteCollection(ctx, metav1.DeleteOptions{}, selector)
	_ = s.DynClientSet.Resource(resource.VMImageGVR).Namespace(namespace).DeleteCollection(ctx, metav1.DeleteOptions{}, selector)
}

func caseResult(name string, start time.Time, err error) *pkgsuites.CaseResult {
	return &pkgsuites.CaseResult{
		CaseName:      name,
		DateTimeStart: start,
		DateTimeEnd:   time.Now(),
		Success:       err == nil,
		CmdResults:    []*pkgsuites.CmdResult{{Cmd: name + " create", Err: errString(err)}},
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
