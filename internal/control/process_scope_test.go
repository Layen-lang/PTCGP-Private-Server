package control

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/configuration"
)

func TestEmulatorProcessHelper(t *testing.T) {
	if os.Getenv("PTCGP_PROCESS_SCOPE_HELPER") != "1" {
		return
	}
	time.Sleep(time.Minute)
}

func TestEmulatorProcessesNeverAdoptAnotherDevicesServer(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestEmulatorProcessHelper$")
	cmd.Env = append(os.Environ(), "PTCGP_PROCESS_SCOPE_HELPER=1")
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	aRoot, bRoot := t.TempDir(), t.TempDir()
	a := &NativeRunner{cfg: configuration.Config{Runtime: configuration.Runtime{RuntimeDirectory: aRoot}}, isolated: true}
	b := &NativeRunner{cfg: configuration.Config{Runtime: configuration.Runtime{RuntimeDirectory: bRoot}}, isolated: true}
	if err := os.WriteFile(filepath.Join(aRoot, "server.pid"), []byte(strconv.Itoa(cmd.Process.Pid)), 0600); err != nil {
		t.Fatal(err)
	}
	if got := a.processPID("server", executable); got != cmd.Process.Pid {
		t.Fatalf("A PID = %d, want %d", got, cmd.Process.Pid)
	}
	if got := b.processPID("server", executable); got != 0 {
		t.Fatalf("B adopted A's PID %d", got)
	}
	if err := b.stopProcess("server", executable); err != nil {
		t.Fatal(err)
	}
	if !processMatches(cmd.Process.Pid, executable) {
		t.Fatal("stopping B terminated A's server")
	}
}
