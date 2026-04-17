//go:build !windows

package process

import "os/exec"

// hideWindow is a no-op on Unix; only Windows needs to hide the console.
func hideWindow(_ *exec.Cmd) {}
