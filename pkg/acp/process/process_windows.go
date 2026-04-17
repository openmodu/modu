//go:build windows

package process

import (
	"os/exec"
	"syscall"
)

// hideWindow sets Windows creation flags so spawning a child console-based
// process (npx, node, etc.) does not flash a console window when this
// binary is run from a desktop app context.
func hideWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= 0x08000000 // CREATE_NO_WINDOW
}
