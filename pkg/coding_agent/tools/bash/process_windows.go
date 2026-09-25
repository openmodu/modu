//go:build windows

package bash

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

const (
	windowsCreateNewProcessGroup = 0x00000200
	windowsCreateNoWindow        = 0x08000000
)

func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windowsCreateNewProcessGroup | windowsCreateNoWindow,
		HideWindow:    true,
	}
}

func killProcessGroup(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid process id %d", pid)
	}

	// os.Process.Kill only terminates bash.exe and can leave commands started
	// by the shell running. taskkill /T applies the same process-tree semantics
	// that a negative process-group kill provides on Unix.
	cmd := exec.Command("taskkill.exe", "/T", "/F", "/PID", strconv.Itoa(pid))
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windowsCreateNoWindow,
		HideWindow:    true,
	}
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		handle, openErr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
		if openErr == nil {
			_ = windows.CloseHandle(handle)
		} else if errors.Is(openErr, windows.ERROR_INVALID_PARAMETER) {
			return nil
		}
		message := strings.TrimSpace(output.String())
		if message == "" {
			return fmt.Errorf("terminate process tree %d: %w", pid, err)
		}
		return fmt.Errorf("terminate process tree %d: %w: %s", pid, err, message)
	}
	return nil
}
