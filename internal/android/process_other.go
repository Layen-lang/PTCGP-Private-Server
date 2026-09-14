//go:build !windows

package android

import "os/exec"

func hideWindow(*exec.Cmd) {}
