package control

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func managedPID(pidFile, executable string) int {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return findManagedProcess(executable)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 || !processMatches(pid, executable) {
		return findManagedProcess(executable)
	}
	return pid
}

func (r *NativeRunner) stopProcess(name, executable string) error {
	pid := managedPID(r.pidFile(name), executable)
	if pid == 0 {
		return nil
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	defer p.Release()
	if err := p.Kill(); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for processMatches(pid, executable) {
		if time.Now().After(deadline) {
			return fmt.Errorf("process %d took too long to stop", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := os.Remove(r.pidFile(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (r *NativeRunner) startProcess(ctx context.Context, name, executable, address string, args ...string) error {
	if managedPID(r.pidFile(name), executable) != 0 {
		return nil
	}
	if err := os.MkdirAll(r.cfg.Runtime.RuntimeDirectory, 0700); err != nil {
		return err
	}
	// Do not mistake another listener for the process being started.
	if conn, err := net.DialTimeout("tcp", loopbackAddress(address), 300*time.Millisecond); err == nil {
		conn.Close()
		return fmt.Errorf("port %s is already in use by an unmanaged process", address)
	}
	out, err := os.Create(filepath.Join(r.cfg.Runtime.RuntimeDirectory, name+".stdout.log"))
	if err != nil {
		return err
	}
	defer out.Close()
	errLogPath := filepath.Join(r.cfg.Runtime.RuntimeDirectory, name+".stderr.log")
	errLog, err := os.Create(errLogPath)
	if err != nil {
		return err
	}
	defer errLog.Close()
	// This child intentionally survives the short-lived CLI command.
	cmd := exec.Command(executable, args...)
	cmd.Dir, cmd.Stdout, cmd.Stderr = r.root, out, errLog
	if err := startDetached(cmd); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if err := os.WriteFile(r.pidFile(name), []byte(strconv.Itoa(cmd.Process.Pid)), 0600); err != nil {
		return errors.Join(err, cmd.Process.Kill())
	}
	waitCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			data, _ := os.ReadFile(errLogPath)
			return fmt.Errorf("%s stopped during startup (%v): %s", name, err, strings.TrimSpace(string(data)))
		case <-waitCtx.Done():
			return errors.Join(waitCtx.Err(), r.stopProcess(name, executable))
		case <-ticker.C:
			conn, err := net.DialTimeout("tcp", loopbackAddress(address), 150*time.Millisecond)
			if err == nil {
				conn.Close()
				return nil
			}
		}
	}
}
