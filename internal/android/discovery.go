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
	return discoverDevices(ctx, runner, requested, packageName, blueStacksEndpoints())
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
		identity, identityErr := runner.Run(ctx, "adb", "-s", serial, "shell", "getprop", "ro.serialno")
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
	return "", fmt.Errorf("multiple ADB devices detected; set android.serial in server.json")
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
