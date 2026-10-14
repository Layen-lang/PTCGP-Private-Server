package android

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Discover refreshes ADB connections on every call, including emulators started
// after the panel. An explicit serial is never replaced with another device.
func Discover(ctx context.Context, runner Runner, requested, packageName string) (string, error) {
	return discoverDevices(ctx, runner, requested, packageName, localEmulatorEndpoints())
}

func localEmulatorEndpoints() []string {
	return append(blueStacksEndpoints(), ldPlayerEndpoints()...)
}

// LDPlayer records the actual ADB forwarding ports in its NAT service logs.
// Wildcard listeners also accept 127.0.0.2, avoiding MuMu's more specific
// 127.0.0.1 listener when both emulators use port 5555.
var ldPlayerForwarding = regexp.MustCompile(`(?m)Event: (Add|Delete) IPv4 port-forwarding rule "([^"]+)"(?:: TCP ([^\s:]*):([0-9]+) -> [0-9.]+:5555\b)?`)
var ldPlayerNATLog = regexp.MustCompile(`^LdNatNetwork[0-9]+\.log$`)

func parseLDPlayerEndpoints(data []byte) []string {
	rules := make(map[string]string)
	for _, match := range ldPlayerForwarding.FindAllSubmatch(data, -1) {
		name := string(match[2])
		delete(rules, name)
		if string(match[1]) == "Delete" {
			continue
		}
		port, err := strconv.Atoi(string(match[4]))
		if err != nil || port <= 0 || port > 65535 {
			continue
		}
		host := string(match[3])
		switch host {
		case "", "0.0.0.0":
			host = "127.0.0.2"
		case "127.0.0.1", "127.0.0.2":
		default:
			continue
		}
		rules[name] = net.JoinHostPort(host, strconv.Itoa(port))
	}
	seen := make(map[string]bool)
	var endpoints []string
	for _, endpoint := range rules {
		if !seen[endpoint] {
			seen[endpoint] = true
			endpoints = append(endpoints, endpoint)
		}
	}
	sort.Strings(endpoints)
	return endpoints
}

func ldPlayerEndpoints() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	root := filepath.Join(home, ".Ld9VirtualBox")
	return ldPlayerEndpointsIn(root)
}

func ldPlayerEndpointsIn(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	seen := make(map[string]bool)
	var endpoints []string
	for _, entry := range entries {
		if entry.IsDir() || !ldPlayerNATLog.MatchString(entry.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, entry.Name()))
		if err != nil {
			continue
		}
		for _, endpoint := range parseLDPlayerEndpoints(data) {
			if !seen[endpoint] {
				seen[endpoint] = true
				endpoints = append(endpoints, endpoint)
			}
		}
	}
	return endpoints
}

func discoverDevices(ctx context.Context, runner Runner, requested, packageName string, endpoints []string) (string, error) {
	type device struct {
		serial     string
		identity   string
		compatible bool
		preferred  bool
	}
	state := func(serial string) bool {
		out, err := runner.Run(ctx, "adb", "-s", serial, "get-state")
		return err == nil && strings.TrimSpace(string(out)) == "device"
	}
	connect := func(endpoint string) {
		probeCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		defer cancel()
		// A failed connection is normal while the emulator is off or booting.
		_, _ = runner.Run(probeCtx, "adb", "connect", endpoint)
	}
	if requested = strings.TrimSpace(requested); requested != "" {
		if !state(requested) {
			if _, _, err := net.SplitHostPort(requested); err == nil {
				connect(requested)
			}
		}
		if state(requested) {
			return requested, nil
		}
		return "", fmt.Errorf("ADB device %s unavailable", requested)
	}
	// Revisit configured local ports even if another ADB device is connected.
	// Multiple compatible devices must stay ambiguous, never chosen arbitrarily.
	for _, endpoint := range endpoints {
		connect(endpoint)
	}
	out, err := runner.Run(ctx, "adb", "devices", "-l")
	if err != nil {
		return "", fmt.Errorf("list ADB devices: %w", err)
	}
	preferred := make(map[string]bool, len(endpoints))
	for _, endpoint := range endpoints {
		preferred[endpoint] = true
	}
	var devices []device
	seen := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[1] != "device" || seen[fields[0]] {
			continue
		}
		serial := fields[0]
		seen[serial] = true
		probe, err := runner.Run(ctx, "adb", "-s", serial, "shell", "pm", "path", packageName)
		candidate := device{
			serial:     serial,
			compatible: err == nil && strings.Contains(string(probe), "package:"),
			preferred:  preferred[serial],
		}
		// A single emulator can be advertised by ADB through several aliases
		// (for example emulator-5554 plus one or more localhost ports). Modern
		// MuMu releases also leave ro.serialno empty. The kernel boot ID is shared
		// by every transport to the same running Android instance while remaining
		// distinct across concurrently running emulators.
		identity, identityErr := runner.Run(ctx, "adb", "-s", serial, "shell", "cat", "/proc/sys/kernel/random/boot_id")
		if identityErr != nil || strings.TrimSpace(string(identity)) == "" {
			identity, identityErr = runner.Run(ctx, "adb", "-s", serial, "shell", "getprop", "ro.serialno")
		}
		candidate.identity = strings.TrimSpace(string(identity))
		if identityErr != nil || candidate.identity == "" {
			candidate.identity = "adb:" + serial
		}
		devices = append(devices, candidate)
	}
	if len(devices) == 0 {
		return "", fmt.Errorf("no ADB emulator connected")
	}
	selected := devices
	var compatible []device
	for _, candidate := range devices {
		if candidate.compatible {
			compatible = append(compatible, candidate)
		}
	}
	if len(compatible) > 0 {
		selected = compatible
	}
	identities := make(map[string][]device)
	for _, candidate := range selected {
		identities[candidate.identity] = append(identities[candidate.identity], candidate)
	}
	if len(identities) == 1 {
		for _, aliases := range identities {
			for _, candidate := range aliases {
				if candidate.preferred {
					return candidate.serial, nil
				}
			}
			return aliases[0].serial, nil
		}
	}
	return "", fmt.Errorf("multiple ADB devices detected; choose an emulator in the panel or specify its serial for this command")
}

var adbPortLine = regexp.MustCompile(`(?m)^bst\.instance\.[^.]+\.(?:status\.)?adb_port="([0-9]+)"\s*$`)

func parseBlueStacksEndpoints(data []byte) []string {
	seen := make(map[int]bool)
	for _, match := range adbPortLine.FindAllSubmatch(data, -1) {
		port, err := strconv.Atoi(string(match[1]))
		if err == nil && port > 0 && port <= 65535 {
			seen[port] = true
		}
	}
	ports := make([]int, 0, len(seen))
	for port := range seen {
		ports = append(ports, port)
	}
	sort.Ints(ports)
	var endpoints []string
	for _, port := range ports {
		endpoints = append(endpoints, net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	}
	return endpoints
}

func blueStacksEndpoints() []string {
	root := os.Getenv("ProgramData")
	if root == "" {
		return nil
	}
	var endpoints []string
	seen := make(map[string]bool)
	for _, folder := range []string{"BlueStacks_nxt", "BlueStacks"} {
		data, err := os.ReadFile(filepath.Join(root, folder, "bluestacks.conf"))
		if err != nil {
			continue
		}
		for _, endpoint := range parseBlueStacksEndpoints(data) {
			if !seen[endpoint] {
				endpoints = append(endpoints, endpoint)
				seen[endpoint] = true
			}
		}
	}
	return endpoints
}
