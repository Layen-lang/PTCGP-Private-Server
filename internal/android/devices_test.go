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
			if got := endpointAddresses(parseLDPlayerEndpoints([]byte(test.log))); !reflect.DeepEqual(got, test.want) {
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
	if got, want := endpointAddresses(ldPlayerEndpointsIn(root)), []string{"127.0.0.2:5555"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("endpoints = %v, want %v", got, want)
	}
	log = []byte(`Event: Add IPv4 port-forwarding rule "two": TCP :5557 -> 172.16.1.5:5555`)
	if err := os.WriteFile(filepath.Join(root, "LdNatNetwork1.log"), log, 0600); err != nil {
		t.Fatal(err)
	}
	if got, want := endpointAddresses(ldPlayerEndpointsIn(root)), []string{"127.0.0.2:5555", "127.0.0.2:5557"}; !reflect.DeepEqual(got, want) {
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
		// A stopped emulator drops its ADB connection.
		if r.ldConnected && r.ldBooted {
			list += "127.0.0.2:5555 device\n"
		}
		return []byte(list), nil
	case "-s 127.0.0.1:5555 shell cat /proc/sys/kernel/random/boot_id", "-s emulator-5554 shell cat /proc/sys/kernel/random/boot_id":
		return []byte("mumu-boot-id"), nil
	case "-s 127.0.0.2:5555 shell cat /proc/sys/kernel/random/boot_id":
		return []byte("ldplayer-boot-id"), nil
	case "-s 127.0.0.1:5555 shell " + identityCommand, "-s emulator-5554 shell " + identityCommand:
		return properties("MuMu model", "x86_64"), nil
	case "-s 127.0.0.2:5555 shell " + identityCommand:
		return properties("LDPlayer model", "x86_64"), nil
	default:
		return nil, fmt.Errorf("unexpected command: %s", call)
	}
}

func TestDeviceListFindsLDPlayerAfterMuMuIsAlreadyConnected(t *testing.T) {
	runner := &lateDeviceRunner{}
	endpoints := []emulatorEndpoint{{"127.0.0.2:5555", emulatorLDPlayer, "#0"}}
	cache := &identityCache{}
	first, err := listDevices(context.Background(), runner, endpoints, cache)
	if err != nil || len(first) != 1 || !runner.probed {
		t.Fatalf("initial list = %v, err = %v, probed = %v", first, err, runner.probed)
	}
	runner.ldBooted = true
	second, err := listDevices(context.Background(), runner, endpoints, cache)
	want := []Device{
		{Serial: "127.0.0.1:5555", Name: "MuMu model", State: "device", Aliases: []string{"127.0.0.1:5555", "emulator-5554"}, Kind: "emulator", Model: "MuMu model"},
		{Serial: "127.0.0.2:5555", Name: "LDPlayer model", State: "device", Kind: "emulator", Emulator: emulatorLDPlayer, Instance: "#0", Model: "LDPlayer model"},
	}
	if err != nil || !reflect.DeepEqual(second, want) {
		t.Fatalf("refreshed list = %v, err = %v, want %v", second, err, want)
	}
	runner.ldBooted = false
	third, err := listDevices(context.Background(), runner, endpoints, cache)
	if err != nil || !reflect.DeepEqual(third, first) {
		t.Fatalf("list after stopping LDPlayer = %v, err = %v, want %v", third, err, first)
	}
}

type countingDeviceRunner struct {
	boot, model string
	calls       []string
}

func (r *countingDeviceRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	call := strings.Join(args, " ")
	r.calls = append(r.calls, call)
	switch call {
	case "devices -l":
		// ADB keeps the transport_id when it reconnects a TCP device.
		return []byte("List of devices attached\n127.0.0.1:5555 device product:x model:y transport_id:1\n"), nil
	case "-s 127.0.0.1:5555 shell cat /proc/sys/kernel/random/boot_id":
		return []byte(r.boot), nil
	case "-s 127.0.0.1:5555 shell " + identityCommand:
		return properties(r.model, "x86_64"), nil
	default:
		return nil, fmt.Errorf("unexpected command: %s", call)
	}
}

func TestDeviceListAsksTheModelOncePerBoot(t *testing.T) {
	runner := &countingDeviceRunner{boot: "bluestacks-boot", model: "BlueStacks model"}
	cache := &identityCache{}
	endpoints := []emulatorEndpoint{{"127.0.0.1:5555", emulatorBlueStacks, "BlueStacks App Player"}}
	want := []Device{{Serial: "127.0.0.1:5555", Name: "BlueStacks model", State: "device", Kind: "emulator", Emulator: emulatorBlueStacks, Instance: "BlueStacks App Player", Model: "BlueStacks model"}}
	for range 2 {
		devices, err := listDevices(context.Background(), runner, endpoints, cache)
		if err != nil || !reflect.DeepEqual(devices, want) {
			t.Fatalf("devices = %v, err = %v", devices, err)
		}
	}
	// The connected endpoint is not reconnected; its model is asked once.
	wantCalls := "devices -l\n-s 127.0.0.1:5555 shell cat /proc/sys/kernel/random/boot_id\n-s 127.0.0.1:5555 shell " + identityCommand + "\n" +
		"devices -l\n-s 127.0.0.1:5555 shell cat /proc/sys/kernel/random/boot_id"
	if got := strings.Join(runner.calls, "\n"); got != wantCalls {
		t.Fatalf("calls =\n%s", got)
	}
	// BlueStacks stops and ADB silently reconnects the same serial and
	// transport to another emulator listening on that port.
	runner.boot, runner.model = "ldplayer-boot", "LDPlayer model"
	devices, err := listDevices(context.Background(), runner, endpoints, cache)
	want = []Device{{Serial: "127.0.0.1:5555", Name: "LDPlayer model", State: "device", Kind: "emulator", Emulator: emulatorBlueStacks, Instance: "BlueStacks App Player", Model: "LDPlayer model"}}
	if err != nil || !reflect.DeepEqual(devices, want) {
		t.Fatalf("devices after reconnection = %v, err = %v, want %v", devices, err, want)
	}
}

func TestMuMuEndpointsReadInstanceConfigs(t *testing.T) {
	root := t.TempDir()
	write := func(instance, body string) {
		dir := filepath.Join(root, "vms", instance, "configs")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "vm_config.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("MuMuPlayerGlobal-12.0-0", `{"vm":{"nat":{"port_forward":{"adb":{"host_port":"16384"},"api":{"host_port":"17425"}}}}}`)
	write("MuMuPlayerGlobal-12.0-1", `{"vm":{"nat":{"port_forward":{"adb":{"host_ip":"127.0.0.1","host_port":"16416","guest_port":"5555"}}}}}`)
	write("MuMuPlayerGlobal-12.0-2", `{"vm":{"nat":{"port_forward":{"adb":{"host_ip":"192.168.1.5","host_port":"16448"}}}}}`)
	write("broken", `{`)
	extra := filepath.Join(root, "vms", "MuMuPlayerGlobal-12.0-1", "configs", "extra_config.json")
	if err := os.WriteFile(extra, []byte(`{"playerName": "Farm"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := muMuEndpointsIn([]string{root, root + string(filepath.Separator)})
	if want := []emulatorEndpoint{{"127.0.0.1:16384", emulatorMuMu, "#0"}, {"127.0.0.1:16416", emulatorMuMu, "Farm #1"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("MuMu endpoints = %v, want %v", got, want)
	}
}

// properties renders getprop output followed by the device name line.
func properties(model, abi string, extra ...string) []byte {
	lines := []string{"[ro.product.model]: [" + model + "]", "[ro.product.cpu.abi]: [" + abi + "]"}
	lines = append(lines, extra...)
	return []byte(strings.Join(lines, "\n") + "\n" + identityMarker + "\n" + model + "\n")
}

func TestParseIdentity(t *testing.T) {
	for _, test := range []struct {
		name   string
		output []byte
		want   deviceIdentity
	}{
		// Captured from LDPlayer 9, which poses as a Redmi phone.
		{"LDPlayer", properties("22127RK46C", "x86_64", "[ro.product.manufacturer]: [Redmi]", "[init.svc.ldinit]: [running]"),
			deviceIdentity{name: "22127RK46C", model: "22127RK46C", emulator: emulatorLDPlayer, virtual: true}},
		{"unknown emulator", properties("SM-S918B", "x86_64"), deviceIdentity{name: "SM-S918B", model: "SM-S918B", virtual: true}},
		// Captured from MuMu Player 12 and BlueStacks 5.
		{"MuMu", properties("SM-A156E", "x86_64", "[init.svc.nemuinit]: [running]"), deviceIdentity{name: "SM-A156E", model: "SM-A156E", emulator: emulatorMuMu, virtual: true}},
		{"BlueStacks", properties("2107113SI", "x86_64", "[init.svc.tombstoned]: [running]", "[init.svc.bstsvcmgrtest]: [running]", "[ro.bionic.ld.warning]: [1]"),
			deviceIdentity{name: "2107113SI", model: "2107113SI", emulator: emulatorBlueStacks, virtual: true}},
		{"tombstoned is not BlueStacks", properties("Pixel 7", "arm64-v8a", "[init.svc.tombstoned]: [running]"), deviceIdentity{name: "Pixel 7", model: "Pixel 7"}},
		{"phone market name", properties("M2101K9G", "arm64-v8a", "[ro.product.marketname]: [Mi 11 Lite 5G NE]"),
			deviceIdentity{name: "Mi 11 Lite 5G NE", model: "M2101K9G"}},
		{"phone device name", []byte("[ro.product.model]: [M2101K9G]\n[ro.product.cpu.abi]: [arm64-v8a]\n[ro.product.marketname]: [Mi 11 Lite]\n" + identityMarker + "\nMatis phone\n"),
			deviceIdentity{name: "Matis phone", model: "M2101K9G"}},
		{"device name unavailable", []byte("[ro.product.model]: [Pixel 7]\n[ro.product.cpu.abi]: [arm64-v8a]\n" + identityMarker + "\nnull\n"),
			deviceIdentity{name: "Pixel 7", model: "Pixel 7"}},
		{"still booting", []byte(identityMarker + "\n"), deviceIdentity{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := parseIdentity(string(test.output)); got != test.want {
				t.Fatalf("identity = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestDescribeDevice(t *testing.T) {
	sources := map[string]emulatorEndpoint{
		"127.0.0.1:5555":  {"127.0.0.1:5555", emulatorBlueStacks, "BlueStacks App Player"},
		"127.0.0.2:5555":  {"127.0.0.2:5555", emulatorLDPlayer, "#0"},
		"127.0.0.1:16416": {"127.0.0.1:16416", emulatorMuMu, "Farm #1"},
	}
	ldplayer := deviceIdentity{emulator: emulatorLDPlayer, virtual: true}
	for _, test := range []struct {
		name                     string
		device                   Device
		identity                 deviceIdentity
		kind, emulator, instance string
	}{
		// LDPlayer also answers on BlueStacks' 127.0.0.1:5555 listener.
		{"shared port", Device{Serial: "127.0.0.1:5555", Aliases: []string{"127.0.0.1:5555", "127.0.0.2:5555", "emulator-5554"}}, ldplayer, "emulator", emulatorLDPlayer, "#0"},
		{"shared port without ADB marker", Device{Serial: "127.0.0.1:5555", Aliases: []string{"127.0.0.1:5555", "127.0.0.2:5555"}}, deviceIdentity{virtual: true}, "emulator", "", ""},
		{"recognised over ADB only", Device{Serial: "emulator-5556"}, ldplayer, "emulator", emulatorLDPlayer, ""},
		{"configured port", Device{Serial: "127.0.0.1:16416"}, deviceIdentity{virtual: true}, "emulator", emulatorMuMu, "Farm #1"},
		{"offline local port", Device{Serial: "127.0.0.1:7555", State: "offline"}, deviceIdentity{}, "emulator", "", ""},
		{"USB phone", Device{Serial: "a1b64c35"}, deviceIdentity{}, "usb", "", ""},
		{"Wi-Fi phone", Device{Serial: "192.168.1.189:42897", Aliases: []string{"192.168.1.189:42897", "adb-a1b64c35-x7TBjW._adb-tls-connect._tcp"}}, deviceIdentity{}, "wifi", "", ""},
		{"phone on USB and Wi-Fi", Device{Serial: "192.168.1.189:42897", Aliases: []string{"192.168.1.189:42897", "a1b64c35"}}, deviceIdentity{}, "usb", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			device := test.device
			describeDevice(&device, test.identity, sources)
			if device.Kind != test.kind || device.Emulator != test.emulator || device.Instance != test.instance {
				t.Fatalf("kind/emulator/instance = %q/%q/%q, want %q/%q/%q", device.Kind, device.Emulator, device.Instance, test.kind, test.emulator, test.instance)
			}
		})
	}
}

func TestLDPlayerRenamedInstances(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config", "leidian1.config"), []byte(`{"statusSettings.playerName": "Farm"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config", "leidian0.config"), []byte(`{"statusSettings.closeOption": 0}`), 0o644); err != nil {
		t.Fatal(err)
	}
	endpoints := []emulatorEndpoint{{"127.0.0.2:5555", emulatorLDPlayer, "#0"}, {"127.0.0.2:5557", emulatorLDPlayer, "#1"}, {"127.0.0.2:5559", emulatorLDPlayer, ""}}
	nameLDPlayerInstances(endpoints, []string{dir})
	want := []emulatorEndpoint{{"127.0.0.2:5555", emulatorLDPlayer, "#0"}, {"127.0.0.2:5557", emulatorLDPlayer, "Farm #1"}, {"127.0.0.2:5559", emulatorLDPlayer, ""}}
	if !reflect.DeepEqual(endpoints, want) {
		t.Fatalf("instances = %v, want %v", endpoints, want)
	}
}
