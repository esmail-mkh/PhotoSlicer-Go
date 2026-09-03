//go:build !windows

package enhancer

import (
	"os/exec"
)

// prepareCommand is a no-op on non-Windows platforms.
func prepareCommand(cmd *exec.Cmd) {
}
