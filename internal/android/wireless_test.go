package android

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type wirelessRunner struct {
	pair, connect, mdns string
	calls               []string
}

func (r *wirelessRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	call := strings.Join(args, " ")
	r.calls = append(r.calls, call)
	switch {
	case strings.HasPrefix(call, "pair "):
		return []byte(r.pair), nil
	case strings.HasPrefix(call, "connect "):
		return []byte(r.connect), nil
	case call == "mdns services":
		return []byte(r.mdns), nil
	}
	return nil, fmt.Errorf("unexpected command: %s", call)
}

func TestWirelessAddressRejectsInvalidInput(t *testing.T) {
	for _, value := range []string{"", "192.168.1.50", "192.168.1.50:0", "192.168.1.50:99999", "-s:5555", "a b:5555", ":5555"} {
		if _, err := WirelessAddress(value); err == nil {
			t.Errorf("%q was accepted", value)
		}
	}
	if got, err := WirelessAddress(" 192.168.1.50:037099 "); err != nil || got != "192.168.1.50:37099" {
		t.Fatalf("address = %q, err = %v", got, err)
	}
}

func TestWirelessPairConnectsToTheAnnouncedPort(t *testing.T) {
	runner := &wirelessRunner{
		pair:    "Successfully paired to 192.168.1.50:37099 [guid=adb-R58M-abc]",
		connect: "connected to 192.168.1.50:41815",
		mdns: "List of discovered mdns services\n" +
			"adb-OTHER-x\t_adb-tls-connect._tcp\t192.168.1.77:40001\n" +
			"adb-R58M-abc\t_adb-tls-pairing._tcp\t192.168.1.50:37099\n" +
			"adb-R58M-abc\t_adb-tls-connect._tcp\t192.168.1.50:41815\n",
	}
	serial, err := WirelessPair(context.Background(), runner, "192.168.1.50:37099", "123456")
	if err != nil || serial != "192.168.1.50:41815" {
		t.Fatalf("serial = %q, err = %v, calls = %v", serial, err, runner.calls)
	}
	if runner.calls[0] != "pair 192.168.1.50:37099 123456" || runner.calls[2] != "connect 192.168.1.50:41815" {
		t.Fatalf("calls = %v", runner.calls)
	}
}

func TestWirelessPairReportsFailures(t *testing.T) {
	runner := &wirelessRunner{pair: "Failed: Wrong password or connection was dropped."}
	if _, err := WirelessPair(context.Background(), runner, "192.168.1.50:37099", "123456"); err == nil || !strings.Contains(err.Error(), "Wrong password") {
		t.Fatalf("err = %v", err)
	}
	if _, err := WirelessPair(context.Background(), runner, "192.168.1.50:37099", "12a456"); err == nil || len(runner.calls) != 1 {
		t.Fatalf("an invalid code reached ADB: err = %v, calls = %v", err, runner.calls)
	}
}

func TestWirelessConnect(t *testing.T) {
	runner := &wirelessRunner{connect: "already connected to 192.168.1.50:41815"}
	if serial, err := WirelessConnect(context.Background(), runner, "192.168.1.50:41815"); err != nil || serial != "192.168.1.50:41815" {
		t.Fatalf("serial = %q, err = %v", serial, err)
	}
	runner.connect = "failed to connect to '192.168.1.50:41815': Connection refused"
	if _, err := WirelessConnect(context.Background(), runner, "192.168.1.50:41815"); err == nil {
		t.Fatal("a refused connection was accepted")
	}
}
