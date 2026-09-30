//go:build windows

package wda

// lockFile is a no-op on Windows, which has no iOS simulators to build for.
func lockFile(string) (func(), error) { return func() {}, nil }
