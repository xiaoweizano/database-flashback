package pitr

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTransitionValid_ValidTransitions(t *testing.T) {
	tests := []struct {
		from, to OperationState
		valid    bool
	}{
		// Valid transitions
		{StatePreflight, StateConfirmed, true},
		{StatePreflight, StateCancelled, true},
		{StateConfirmed, StateParsing, true},
		{StateConfirmed, StateCancelled, true},
		{StateParsing, StatePreviewed, true},
		{StateParsing, StateFailed, true},
		{StatePreviewed, StateExecuting, true},
		{StatePreviewed, StateCancelled, true},
		{StateExecuting, StateCompleted, true},
		{StateExecuting, StateFailed, true},
		// Invalid transitions
		{StatePreflight, StateCompleted, false},
		{StateConfirmed, StateCompleted, false},
		{StatePreviewed, StateParsing, false},
		{StateCompleted, StateFailed, false},
		{StateFailed, StateCancelled, false},
		{StateCancelled, StatePreflight, false},
		{StateCompleted, StateExecuting, false},
		{StateParsing, StateExecuting, false},
		{StateExecuting, StateCancelled, false},
		{StateExecuting, StatePreviewed, false},
	}

	for _, tc := range tests {
		assert.Equal(t, tc.valid, TransitionValid(tc.from, tc.to),
			"transition %s -> %s should be valid=%v", tc.from, tc.to, tc.valid)
	}
}

func TestIsTerminal(t *testing.T) {
	assert.True(t, IsTerminal(StateCompleted), "completed should be terminal")
	assert.True(t, IsTerminal(StateFailed), "failed should be terminal")
	assert.True(t, IsTerminal(StateCancelled), "cancelled should be terminal")

	assert.False(t, IsTerminal(StatePreflight), "preflight should not be terminal")
	assert.False(t, IsTerminal(StateConfirmed), "confirmed should not be terminal")
	assert.False(t, IsTerminal(StateParsing), "parsing should not be terminal")
	assert.False(t, IsTerminal(StatePreviewed), "previewed should not be terminal")
	assert.False(t, IsTerminal(StateExecuting), "executing should not be terminal")
}

func TestMustTransition_PanicsOnInvalid(t *testing.T) {
	assert.Panics(t, func() {
		MustTransition(StatePreflight, StateCompleted)
	}, "preflight -> completed should panic")
}

func TestMustTransition_PanicsOnTerminalSource(t *testing.T) {
	assert.Panics(t, func() {
		MustTransition(StateCompleted, StateFailed)
	}, "completed -> failed should panic")
}

func TestMustTransition_ValidDoesNotPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		MustTransition(StatePreflight, StateConfirmed)
	}, "preflight -> confirmed should not panic")
}

func TestTransitionValid_AllTerminalStatesAreDeadEnds(t *testing.T) {
	for _, terminal := range []OperationState{StateCompleted, StateFailed, StateCancelled} {
		for _, target := range []OperationState{
			StatePreflight, StateConfirmed, StateParsing,
			StatePreviewed, StateExecuting, StateCompleted,
			StateFailed, StateCancelled,
		} {
			assert.False(t, TransitionValid(terminal, target),
				"no transition from terminal state %s -> %s", terminal, target)
		}
	}
}
