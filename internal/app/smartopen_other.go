//go:build !linux

package app

import "errors"

// Smart open is implemented for Linux first. Everywhere else the discovery finds nothing, so the
// caller falls back to the OS URL handler and behaviour is exactly what it was before.
func findPWALauncher(string) (pwaLauncher, bool) { return pwaLauncher{}, false }

func launchPWALauncher(pwaLauncher) error {
	return errors.New("smart open is not implemented on this platform")
}
