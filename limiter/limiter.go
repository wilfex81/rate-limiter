package limiter

import "time"

// Decideds whether one or more request is allowed right now
// Otherwise the duration says how long to wait before trying again.
type Limiter interface {
	Allow() (bool, time.Duration)
}
