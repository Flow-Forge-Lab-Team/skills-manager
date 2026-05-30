//go:build windows

package cli

import "os/exec"

func configureCommandProcessGroup(cmd *exec.Cmd) {}

func killCommandProcessGroup(cmd *exec.Cmd) {}
