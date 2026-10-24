package gateways

import "time"

// SystemClock is the Clock implementation backed by the wall clock (UTC).
type SystemClock struct{}

// Now returns the current time in UTC.
func (SystemClock) Now() time.Time { return time.Now().UTC() }
