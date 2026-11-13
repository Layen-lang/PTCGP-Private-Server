package control

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}

func detachProcess(cmd *exec.Cmd) {
	// Detach from the invoking console and its job so closing the CLI does not
	// terminate the persistent panel or game server.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_BREAKAWAY_FROM_JOB}
}

func startDetached(cmd *exec.Cmd) error {
	detachProcess(cmd)
	err := cmd.Start()
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		// Some parent jobs forbid breakaway. Keep console detachment and let
		// the persistent parent own the child in that environment.
		cmd.SysProcAttr.CreationFlags &^= windows.CREATE_BREAKAWAY_FROM_JOB
		return cmd.Start()
	}
	return err
}

var allowSetForegroundWindow = windows.NewLazySystemDLL("user32.dll").NewProc("AllowSetForegroundWindow")

const asfwAny = ^uint32(0)

// startWindowed starts an application that shows a window. Unlike
// startDetached it must not request a hidden window: Windows applies that
// startup state to the application's first window.
func startWindowed(cmd *exec.Cmd) error {
	// Let the application bring an already open window to the front.
	_, _, _ = allowSetForegroundWindow.Call(uintptr(asfwAny))
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_BREAKAWAY_FROM_JOB}
	err := cmd.Start()
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		cmd.SysProcAttr.CreationFlags &^= windows.CREATE_BREAKAWAY_FROM_JOB
		return cmd.Start()
	}
	return err
}

func processMatches(pid int, executable string) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	var exit uint32
	if windows.GetExitCodeProcess(handle, &exit) != nil || exit != 259 {
		return false
	}
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size) != nil {
		return false
	}
	return strings.EqualFold(filepath.Clean(windows.UTF16ToString(buffer[:size])), filepath.Clean(executable))
}

func findManagedProcess(executable string) int {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{}
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if int(entry.ProcessID) == os.Getpid() {
			continue
		}
		if strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), filepath.Base(executable)) && processMatches(int(entry.ProcessID), executable) {
			return int(entry.ProcessID)
		}
	}
	return 0
}
