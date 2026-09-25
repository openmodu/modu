//go:build windows

package bash

import (
	"os/exec"
	"testing"
	"time"
)

func TestConfigureProcessGroupWindows(t *testing.T) {
	cmd := exec.Command("bash", "-c", "true")
	configureProcessGroup(cmd)
	if cmd.SysProcAttr == nil {
		t.Fatal("configureProcessGroup should configure Windows process attributes")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Fatal("configureProcessGroup should hide the child console window")
	}
	want := uint32(windowsCreateNewProcessGroup | windowsCreateNoWindow)
	if got := cmd.SysProcAttr.CreationFlags; got&want != want {
		t.Fatalf("CreationFlags = %#x, want flags %#x", got, want)
	}
}

func TestKillProcessGroupWindows(t *testing.T) {
	cmd := exec.Command("bash", "-c", "sleep 30")
	configureProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start bash: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	if err := killProcessGroup(cmd.Process.Pid); err != nil {
		t.Fatalf("kill process group: %v", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("process did not exit after killProcessGroup")
	}
}

func TestKillProcessGroupWindowsAlreadyExited(t *testing.T) {
	const nonexistentPID = 1<<31 - 1
	if err := killProcessGroup(nonexistentPID); err != nil {
		t.Fatalf("killing an exited process should be idempotent: %v", err)
	}
}
