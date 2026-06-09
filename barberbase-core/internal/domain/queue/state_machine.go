package queue

import (
	"errors"
)

var (
	ErrInvalidStateTransition = errors.New("invalid state transition")
	ErrDirectStartNotArrived  = errors.New("direct start requires presence_state=arrived")
)

// ValidateStart returns (directStart bool, err error).
//   state="called"                          → (false, nil)
//   state="waiting", presence="arrived"     → (true,  nil)
//   state="waiting", presence≠"arrived"     → (false, ErrDirectStartNotArrived)
//   any other state                         → (false, ErrInvalidStateTransition)
func ValidateStart(state, presenceState string) (bool, error) {
	switch state {
	case "called":
		return false, nil
	case "waiting":
		if presenceState == "arrived" {
			return true, nil
		}
		return false, ErrDirectStartNotArrived
	default:
		return false, ErrInvalidStateTransition
	}
}
