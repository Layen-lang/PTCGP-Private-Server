package android

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

type discoveryRunner struct {
	booted, connected bool
	extra             string
	identities        map[string]string
	calls             []string
}

func (r *discoveryRunner) Run(ctx context.Context, _ string, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	call := strings.Join(args, " ")
	r.calls = append(r.calls, call)
	switch {
	case call == "connect 127.0.0.1:5555":
		r.connected = r.booted
		if !r.booted {
			return nil, fmt.Errorf("connection refused")
		}
		return []byte("connected"), nil
	case call == "devices -l":
		devices := "List of devices attached\n" + r.extra
		if r.connected {
			devices += "127.0.0.1:5555 device product:phone\n"
		}
		return []byte(devices), nil
	case strings.HasSuffix(call, "get-state"):
		if r.connected && strings.Contains(call, "127.0.0.1:5555") {
			return []byte("device"), nil
		}
		return nil, fmt.Errorf("offline")
	case strings.Contains(call, "shell pm path"):
		return []byte("package:/data/app/game/base.apk"), nil
	case strings.Contains(call, "shell getprop ro.serialno"):
		serial := strings.Fields(call)[1]
		if identity := r.identities[serial]; identity != "" {
			return []byte(identity), nil
		}
		return []byte(serial), nil
	}
	return nil, fmt.Errorf("unexpected command: %s", call)
}

func TestDiscoverLateStartDisconnectAndRestart(t *testing.T) {
	r := &discoveryRunner{}
	for _, booted := range []bool{false, true, false, true} {
		r.booted = booted
		serial, err := discoverDevices(context.Background(), r, "", "game", []string{"127.0.0.1:5555"})
		if booted && (err != nil || serial != "127.0.0.1:5555") {
			t.Fatalf("booted: serial=%q err=%v", serial, err)
		}
		if !booted && err == nil {
			t.Fatal("stopped emulator was detected")
		}
	}
}

func TestDiscoverRejectsAmbiguityAndHonorsExplicitSerial(t *testing.T) {
	r := &discoveryRunner{booted: true, extra: "other device\nunauthorized unauthorized\noffline offline\n", identities: map[string]string{"other": "other-phone", "127.0.0.1:5555": "bluestacks"}}
	if _, err := discoverDevices(context.Background(), r, "", "game", []string{"127.0.0.1:5555"}); err == nil {
		t.Fatal("two compatible devices should require selection")
	}
	r.calls = nil
	if _, err := discoverDevices(context.Background(), r, "missing-usb", "game", []string{"127.0.0.1:5555"}); err == nil {
		t.Fatal("explicit missing device was replaced")
	}
	for _, call := range r.calls {
		if strings.HasPrefix(call, "connect") || call == "devices -l" {
			t.Fatalf("unexpected fallback: %s", call)
		}
	}
}

func TestDiscoverDeduplicatesAliasesOfSameEmulator(t *testing.T) {
	r := &discoveryRunner{
		booted: true,
		extra:  "emulator-5554 device product:phone\n",
		identities: map[string]string{
			"emulator-5554":  "bluestacks-device",
			"127.0.0.1:5555": "bluestacks-device",
		},
	}
	serial, err := discoverDevices(context.Background(), r, "", "game", []string{"127.0.0.1:5555"})
	if err != nil || serial != "127.0.0.1:5555" {
		t.Fatalf("serial=%q err=%v", serial, err)
	}
}

func TestExplicitTCPReconnect(t *testing.T) {
	r := &discoveryRunner{booted: true}
	serial, err := discoverDevices(context.Background(), r, "127.0.0.1:5555", "game", nil)
	if err != nil || serial != "127.0.0.1:5555" {
		t.Fatalf("serial=%q err=%v", serial, err)
	}
}

func TestBlueStacksPortsAreValidatedAndDeduplicated(t *testing.T) {
	data := []byte("bst.instance.Pie64.adb_port=\"5555\"\r\nbst.instance.Pie64.status.adb_port=\"5555\"\r\nbst.instance.Second.adb_port=\"5565\"\nbst.instance.Bad.adb_port=\"65536\"\nbst.instance.Off.adb_port=\"0\"\n")
	if got, want := parseBlueStacksEndpoints(data), []string{"127.0.0.1:5555", "127.0.0.1:5565"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ports=%v want=%v", got, want)
	}
}

func TestDiscoveryCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := discoverDevices(ctx, &discoveryRunner{}, "", "game", nil); err == nil {
		t.Fatal("cancelled discovery succeeded")
	}
}
