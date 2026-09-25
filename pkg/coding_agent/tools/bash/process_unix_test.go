//go:build !windows

package bash

import (
	"os/exec"
	"testing"
)

func TestConfigureProcessGroupUnix(t *testing.T) {
	cmd := exec.Command("bash", "-c", "true")
	configureProcessGroup(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatal("configureProcessGroup should start the command in a new process group")
	}
}
