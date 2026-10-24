package interfaces

import "time"

// Clock is a port so use cases never call time.Now directly.
type Clock interface {
	Now() time.Time
}
