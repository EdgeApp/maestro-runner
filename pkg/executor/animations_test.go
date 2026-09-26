package executor

import (
	"testing"

	"github.com/devicelab-dev/maestro-runner/pkg/core"
	"github.com/devicelab-dev/maestro-runner/pkg/flow"
)

// animDriver records disableAnimations switches.
type animDriver struct {
	*mockDriver
	calls []bool
}

func (a *animDriver) SetAnimationsDisabled(disabled bool) error {
	a.calls = append(a.calls, disabled)
	return nil
}

var _ core.AnimationController = (*animDriver)(nil)

// The run switches animations off at the start and back on at the end; a
// flow's own setting overrides it for that flow only.
func TestDisableAnimations(t *testing.T) {
	step := &flow.EvalScriptStep{BaseStep: flow.BaseStep{StepType: flow.StepEvalScript}, Script: "${1}"}
	for _, tc := range []struct {
		name  string
		run   bool
		flow  *bool
		calls []bool
	}{
		{"off", false, nil, nil},
		{"run", true, nil, []bool{true, false}},
		{"flow only", false, boolPtr(true), []bool{true, false}},
		{"flow re-enables", true, boolPtr(false), []bool{true, false, true, false}},
		{"flow same as run", true, boolPtr(true), []bool{true, false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &animDriver{mockDriver: &mockDriver{}}
			f := flow.Flow{SourcePath: "t.yaml", Config: flow.Config{Name: "anim", DisableAnimations: tc.flow}, Steps: []flow.Step{step}}
			r := New(d, RunnerConfig{OutputDir: t.TempDir(), DisableAnimations: tc.run})
			if _, err := r.Run(t.Context(), []flow.Flow{f}); err != nil {
				t.Fatal(err)
			}
			if len(d.calls) != len(tc.calls) {
				t.Fatalf("calls = %v, want %v", d.calls, tc.calls)
			}
			for i := range tc.calls {
				if d.calls[i] != tc.calls[i] {
					t.Fatalf("calls = %v, want %v", d.calls, tc.calls)
				}
			}
		})
	}
}
