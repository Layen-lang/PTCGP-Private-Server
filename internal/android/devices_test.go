package android

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLDPlayerForwardingEndpoints(t *testing.T) {
	for _, test := range []struct {
		name string
		log  string
		want []string
	}{
		{"wildcard avoids MuMu", `Event: Add IPv4 port-forwarding rule "tcp_5555_0": TCP :5555 -> 172.16.1.4:5555`, []string{"127.0.0.2:5555"}},
		{"multiple instances", "Event: Add IPv4 port-forwarding rule \"one\": TCP 0.0.0.0:5555 -> 172.16.1.4:5555\nEvent: Add IPv4 port-forwarding rule \"two\": TCP :5557 -> 172.16.1.5:5555", []string{"127.0.0.2:5555", "127.0.0.2:5557"}},
		{"explicit loopback", `Event: Add IPv4 port-forwarding rule "one": TCP 127.0.0.1:5565 -> 172.16.1.4:5555`, []string{"127.0.0.1:5565"}},
		{"removed rule", "Event: Add IPv4 port-forwarding rule \"one\": TCP :5555 -> 172.16.1.4:5555\nEvent: Delete IPv4 port-forwarding rule \"one\"", nil},
		{"duplicate endpoint", "Event: Add IPv4 port-forwarding rule \"one\": TCP :5555 -> 172.16.1.4:5555\nEvent: Add IPv4 port-forwarding rule \"two\": TCP :5555 -> 172.16.1.4:5555", []string{"127.0.0.2:5555"}},
		{"non ADB forwarding", `Event: Add IPv4 port-forwarding rule "ssh": TCP :5555 -> 172.16.1.4:22`, nil},
		{"guest port prefix", `Event: Add IPv4 port-forwarding rule "other": TCP :5555 -> 172.16.1.4:55550`, nil},
		{"remote address", `Event: Add IPv4 port-forwarding rule "one": TCP 192.168.1.10:5555 -> 172.16.1.4:5555`, nil},
		{"invalid port", `Event: Add IPv4 port-forwarding rule "one": TCP :65536 -> 172.16.1.4:5555`, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := parseLDPlayerEndpoints([]byte(test.log)); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("endpoints = %v, want %v", got, test.want)
			}
		})
	}
}

func TestLDPlayerEndpointsReadNewlyStartedInstances(t *testing.T) {
	root := t.TempDir()
	if got := ldPlayerEndpointsIn(root); len(got) != 0 {
		t.Fatalf("empty log directory returned %v", got)
	}
	log := []byte(`Event: Add IPv4 port-forwarding rule "one": TCP :5555 -> 172.16.1.4:5555`)
	for _, name := range []string{"LdNatNetwork0.log", "LdNatNetwork1.log", "LdNatNetwork2.log.1", "unrelated.log"} {
		if err := os.WriteFile(filepath.Join(root, name), log, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := ldPlayerEndpointsIn(root), []string{"127.0.0.2:5555"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("endpoints = %v, want %v", got, want)
	}
	log = []byte(`Event: Add IPv4 port-forwarding rule "two": TCP :5557 -> 172.16.1.5:5555`)
	if err := os.WriteFile(filepath.Join(root, "LdNatNetwork1.log"), log, 0600); err != nil {
		t.Fatal(err)
	}
	if got, want := ldPlayerEndpointsIn(root), []string{"127.0.0.2:5555", "127.0.0.2:5557"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("new instance endpoints = %v, want %v", got, want)
	}
}

type lateDeviceRunner struct {
	ldBooted, ldConnected bool
	probed                bool
}

func (r *lateDeviceRunner) Run(ctx context.Context, _ string, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	call := strings.Join(args, " ")
	switch call {
	case "connect 127.0.0.2:5555":
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 1500*time.Millisecond {
			return nil, fmt.Errorf("connection probe is not bounded")
		}
		r.probed = true
		r.ldConnected = r.ldBooted
		if !r.ldBooted {
			return nil, fmt.Errorf("LDPlayer is stopped")
		}
		return []byte("connected"), nil
	case "devices -l":
		list := "List of devices attached\n127.0.0.1:5555 device\nemulator-5554 device\n"
		if r.ldConnected {
			list += "127.0.0.2:5555 device\n"
		}
		return []byte(list), nil
	case "-s 127.0.0.1:5555 shell cat /proc/sys/kernel/random/boot_id", "-s emulator-5554 shell cat /proc/sys/kernel/random/boot_id":
		return []byte("mumu-boot-id"), nil
	case "-s 127.0.0.2:5555 shell cat /proc/sys/kernel/random/boot_id":
		return []byte("ldplayer-boot-id"), nil
	case "-s 127.0.0.1:5555 shell getprop ro.product.model", "-s emulator-5554 shell getprop ro.product.model":
		return []byte("MuMu model"), nil
	case "-s 127.0.0.2:5555 shell getprop ro.product.model":
		return []byte("LDPlayer model"), nil
	default:
		return nil, fmt.Errorf("unexpected command: %s", call)
	}
}

func TestDeviceListFindsLDPlayerAfterMuMuIsAlreadyConnected(t *testing.T) {
	runner := &lateDeviceRunner{}
	endpoints := []string{"127.0.0.2:5555"}
	first, err := listDevices(context.Background(), runner, endpoints)
	if err != nil || len(first) != 1 || !runner.probed {
		t.Fatalf("initial list = %v, err = %v, probed = %v", first, err, runner.probed)
	}
	runner.ldBooted = true
	second, err := listDevices(context.Background(), runner, endpoints)
	want := []Device{
		{Serial: "127.0.0.1:5555", Name: "MuMu model", State: "device", Aliases: []string{"127.0.0.1:5555", "emulator-5554"}},
		{Serial: "127.0.0.2:5555", Name: "LDPlayer model", State: "device"},
	}
	if err != nil || !reflect.DeepEqual(second, want) {
		t.Fatalf("refreshed list = %v, err = %v, want %v", second, err, want)
	}
	runner.ldBooted = false
	third, err := listDevices(context.Background(), runner, endpoints)
	if err != nil || !reflect.DeepEqual(third, first) {
		t.Fatalf("list after stopping LDPlayer = %v, err = %v, want %v", third, err, first)
	}
}
