package pitr

import "fmt"

// OperationState represents the current state of a PITR recovery operation.
type OperationState string

const (
	StatePreflight OperationState = "preflight"
	StateConfirmed OperationState = "confirmed"
	StateParsing   OperationState = "parsing"
	StatePreviewed OperationState = "previewed"
	StateExecuting OperationState = "executing"
	StateCompleted OperationState = "completed"
	StateFailed    OperationState = "failed"
	StateCancelled OperationState = "cancelled"
)

// validTransitions defines the set of allowed state transitions. Keys are the
// current state; values are the set of states that may be transitioned to.
// Terminal states (completed, failed, cancelled) are not included as sources
// since no transitions are valid from them.
var validTransitions = map[OperationState][]OperationState{
	StatePreflight: {StateConfirmed, StateCancelled},
	StateConfirmed: {StateParsing, StateCancelled},
	StateParsing:   {StatePreviewed, StateFailed},
	StatePreviewed: {StateExecuting, StateCancelled},
	StateExecuting: {StateCompleted, StateFailed},
}

// TransitionValid checks whether moving from current state `from` to new state
// `to` is a permitted transition according to the state machine.
func TransitionValid(from, to OperationState) bool {
	allowed, ok := validTransitions[from]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == to {
			return true
		}
	}
	return false
}

// IsTerminal returns true when `state` is a terminal state (completed, failed,
// or cancelled).
func IsTerminal(state OperationState) bool {
	return state == StateCompleted || state == StateFailed || state == StateCancelled
}

// TryTransitionErr returns a descriptive error if the transition from `from` to
// `to` is invalid.
func TryTransitionErr(from, to OperationState) error {
	if !TransitionValid(from, to) {
		return fmt.Errorf("invalid state transition: %s -> %s", from, to)
	}
	return nil
}
