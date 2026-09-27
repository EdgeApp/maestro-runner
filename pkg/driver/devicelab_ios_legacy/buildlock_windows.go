//go:build windows

package devicelab_ios_legacy

// lockFile is a no-op on Windows, which has no iOS simulators to build for.
func lockFile(string) (func(), error) { return func() {}, nil }
