//go:build !linux

package transcode

import "os/exec"

func setParentDeathSignal(cmd *exec.Cmd) {}
