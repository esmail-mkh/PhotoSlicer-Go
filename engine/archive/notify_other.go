//go:build !windows

package archive

// NotifyShellChange is a no-op on non-Windows platforms.
func NotifyShellChange(path string) {}
