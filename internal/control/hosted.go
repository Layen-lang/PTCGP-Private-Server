package control

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// HostServer runs one device's game server inside the current process until
// ctx is cancelled. The panel sets it so every device's server lives in the
// single ptcgp-server.exe process instead of a child process per device.
// When nil (short-lived command-line use), servers are child processes.
var HostServer func(ctx context.Context, args []string, logger *slog.Logger) error

type hostedServer struct {
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

var hosted = struct {
	sync.Mutex
	servers map[string]*hostedServer
}{servers: map[string]*hostedServer{}}

func (r *NativeRunner) hosts(name string) bool { return name == "server" && HostServer != nil }

func (r *NativeRunner) hostedRunning(name string) bool {
	hosted.Lock()
	defer hosted.Unlock()
	server := hosted.servers[r.pidFile(name)]
	if server == nil {
		return false
	}
	select {
	case <-server.done:
		return false
	default:
		return true
	}
}

func (r *NativeRunner) startHosted(ctx context.Context, name, address string, args []string) error {
	if r.hostedRunning(name) {
		return nil
	}
	if err := os.MkdirAll(r.cfg.Runtime.RuntimeDirectory, 0700); err != nil {
		return err
	}
	if conn, err := net.DialTimeout("tcp", loopbackAddress(address), 300*time.Millisecond); err == nil {
		conn.Close()
		return fmt.Errorf("port %s is already in use by an unmanaged process", address)
	}
	errLogPath := filepath.Join(r.cfg.Runtime.RuntimeDirectory, name+".stderr.log")
	errLog, err := os.Create(errLogPath)
	if err != nil {
		return err
	}
	serverCtx, cancel := context.WithCancel(context.Background())
	server := &hostedServer{cancel: cancel, done: make(chan struct{})}
	logger := slog.New(slog.NewTextHandler(io.Writer(errLog), &slog.HandlerOptions{Level: slog.LevelInfo}))
	go func() {
		defer close(server.done)
		defer errLog.Close()
		server.err = HostServer(serverCtx, args, logger)
		if server.err != nil {
			logger.Error("command failed", "error", server.err)
		}
	}()
	hosted.Lock()
	hosted.servers[r.pidFile(name)] = server
	hosted.Unlock()
	// Command-line status checks in other processes see the hosting process.
	if err := os.WriteFile(r.pidFile(name), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		return errors.Join(err, r.stopHosted(name))
	}
	waitCtx, cancelWait := context.WithTimeout(ctx, 30*time.Second)
	defer cancelWait()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-server.done:
			data, _ := os.ReadFile(errLogPath)
			return fmt.Errorf("%s stopped during startup (%v): %s", name, server.err, lastLine(string(data)))
		case <-waitCtx.Done():
			return errors.Join(waitCtx.Err(), r.stopHosted(name))
		case <-ticker.C:
			if conn, err := net.DialTimeout("tcp", loopbackAddress(address), 150*time.Millisecond); err == nil {
				conn.Close()
				return nil
			}
		}
	}
}

func (r *NativeRunner) stopHosted(name string) error {
	hosted.Lock()
	server := hosted.servers[r.pidFile(name)]
	delete(hosted.servers, r.pidFile(name))
	hosted.Unlock()
	if server != nil {
		server.cancel()
		select {
		case <-server.done:
		case <-time.After(12 * time.Second):
			return fmt.Errorf("%s took too long to stop", name)
		}
	}
	if err := os.Remove(r.pidFile(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// StopHostedServers stops every in-process server, for panel shutdown.
func StopHostedServers() {
	hosted.Lock()
	servers := make([]*hostedServer, 0, len(hosted.servers))
	for key, server := range hosted.servers {
		servers = append(servers, server)
		_ = os.Remove(key)
	}
	hosted.servers = map[string]*hostedServer{}
	hosted.Unlock()
	for _, server := range servers {
		server.cancel()
	}
	for _, server := range servers {
		select {
		case <-server.done:
		case <-time.After(12 * time.Second):
		}
	}
}

func lastLine(text string) string {
	for len(text) > 0 && (text[len(text)-1] == '\n' || text[len(text)-1] == '\r') {
		text = text[:len(text)-1]
	}
	for i := len(text) - 1; i >= 0; i-- {
		if text[i] == '\n' {
			return text[i+1:]
		}
	}
	return text
}
