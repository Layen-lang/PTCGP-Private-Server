package control

import (
	"context"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/configuration"
)

func TestHostedServersRunInsideThePanelProcess(t *testing.T) {
	saved := HostServer
	defer func() { HostServer = saved }()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	var received []string
	HostServer = func(ctx context.Context, args []string, logger *slog.Logger) error {
		received = args
		ln, err := net.Listen("tcp", address)
		if err != nil {
			return err
		}
		<-ctx.Done()
		return ln.Close()
	}
	root := t.TempDir()
	cfg := configuration.Config{Path: filepath.Join(root, "server.json"), Runtime: configuration.Runtime{RuntimeDirectory: filepath.Join(root, "runtime")}}
	first := &NativeRunner{cfg: cfg, root: root, server: filepath.Join(root, "bin", "ptcgp-server.exe")}
	secondCfg := cfg
	secondCfg.Runtime.RuntimeDirectory = filepath.Join(root, "runtime", "emulators", "b")
	second := &NativeRunner{cfg: secondCfg, root: root, server: first.server, isolated: true}

	if err := first.startProcess(context.Background(), "server", first.server, address, "serve", "--address", address); err != nil {
		t.Fatal(err)
	}
	if got := first.processPID("server", first.server); got != os.Getpid() {
		t.Fatalf("hosted PID = %d, want this process %d", got, os.Getpid())
	}
	if second.processPID("server", second.server) != 0 {
		t.Fatal("another device must not see the first device's server")
	}
	if strings.Join(received, " ") != "serve --address "+address {
		t.Fatalf("arguments = %q", received)
	}
	data, err := os.ReadFile(first.pidFile("server"))
	if err != nil || strings.TrimSpace(string(data)) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("PID file = %q, %v", data, err)
	}
	if err := first.stopProcess("server", first.server); err != nil {
		t.Fatal(err)
	}
	if first.processPID("server", first.server) != 0 {
		t.Fatal("server still reported after stop")
	}
	if _, err := os.Stat(first.pidFile("server")); !os.IsNotExist(err) {
		t.Fatalf("PID file left behind: %v", err)
	}
	if conn, err := net.Dial("tcp", address); err == nil {
		conn.Close()
		t.Fatal("listener still open after stop")
	}
}
