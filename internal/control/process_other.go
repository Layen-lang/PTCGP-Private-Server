//go:build !windows

package control

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

func hideWindow(cmd *exec.Cmd) {}

func detachProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

func startDetached(cmd *exec.Cmd) error { detachProcess(cmd); return cmd.Start() }

func processMatches(pid int, executable string) bool {
	actual, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
	return err == nil && filepath.Clean(actual) == filepath.Clean(executable)
}

func findManagedProcess(executable string) int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err == nil && pid > 0 && pid != os.Getpid() && processMatches(pid, executable) {
			return pid
		}
	}
	return 0
}

func openBrowser(address string) error {
	cmd := exec.Command("xdg-open", address)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
