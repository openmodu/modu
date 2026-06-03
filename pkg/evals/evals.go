package evals

import (
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/types"
)

// EvalT is the testing handle passed into each eval case.
type EvalT struct {
	*testing.T
	*Eval
}

// Eval contains the model under test plus the grader model.
type Eval struct {
	Provider     providers.Provider
	Model        *types.Model
	Grader       providers.Provider
	GraderModel  *types.Model
	ProviderSpec ProviderSpec
	GraderSpec   ProviderSpec

	runNumber int
}

// NumEvalsOrSkip returns GOEVALS or skips when evals are disabled.
func NumEvalsOrSkip(t *testing.T) int {
	t.Helper()

	numEvals, err := strconv.Atoi(os.Getenv("GOEVALS"))
	if err != nil || numEvals < 1 {
		t.Skip("Skipping evals. Use GOEVALS=1 to run.")
	}

	return numEvals
}

// NewEval creates an Eval from an explicit provider spec.
func NewEval(spec ProviderSpec) (*Eval, error) {
	provider, err := NewProvider(spec)
	if err != nil {
		return nil, err
	}

	graderSpec := graderSpecFromEnv(spec)
	grader, err := NewProvider(graderSpec)
	if err != nil {
		return nil, fmt.Errorf("create grader: %w", err)
	}

	return &Eval{
		Provider:     provider,
		Model:        spec.Model(),
		Grader:       grader,
		GraderModel:  graderSpec.Model(),
		ProviderSpec: spec,
		GraderSpec:   graderSpec,
	}, nil
}

// Run executes an eval for every provider listed in EVAL_PROVIDER.
func Run(t *testing.T, name string, f func(e *EvalT)) {
	t.Helper()
	numEvals := NumEvalsOrSkip(t)

	specs := providerSpecsFromEnv()
	if len(specs) == 0 {
		t.Fatal("no eval providers configured")
	}

	for _, spec := range specs {
		spec := spec
		eval, err := NewEval(spec)
		if err != nil {
			t.Logf("Skipping %s provider: %v", spec.ProviderID, err)
			continue
		}

		testName := fmt.Sprintf("[%s/%s] %s", spec.ProviderID, spec.ModelID, name)
		t.Run(testName, func(t *testing.T) {
			e := &EvalT{T: t, Eval: eval}
			for i := 0; i < numEvals; i++ {
				e.runNumber = i
				f(e)
			}
		})
	}
}
