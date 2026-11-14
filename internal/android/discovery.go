package android

import (
	"context"
	"encoding/json"
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
	return discoverDevices(ctx, runner, requested, packageName, endpointAddresses(localEmulatorEndpoints()))
}

// Emulator names reported to the panel.
const (
	emulatorMuMu       = "MuMu"
	emulatorLDPlayer   = "LDPlayer"
	emulatorBlueStacks = "BlueStacks"
)

// emulatorEndpoint is a local ADB port with the emulator instance that
// forwards it, as read from that emulator's own configuration.
type emulatorEndpoint struct {
	address, emulator, instance string
}

func endpointAddresses(endpoints []emulatorEndpoint) []string {
	var addresses []string
	for _, endpoint := range endpoints {
		addresses = append(addresses, endpoint.address)
	}
	return addresses
}

func localEmulatorEndpoints() []emulatorEndpoint {
	endpoints := append(blueStacksEndpoints(), ldPlayerEndpoints()...)
	return append(endpoints, muMuEndpoints()...)
}

// MuMu Player (12 and later) keeps each instance's ADB forwarding port in
// vms/<instance>/configs/vm_config.json under its install directory.
type muMuVMConfig struct {
	VM struct {
		NAT struct {
			PortForward struct {
				ADB struct {
					HostIP   string `json:"host_ip"`
					HostPort string `json:"host_port"`
				} `json:"adb"`
			} `json:"port_forward"`
		} `json:"nat"`
	} `json:"vm"`
}

func parseMuMuEndpoint(data []byte) string {
	var config muMuVMConfig
	if json.Unmarshal(data, &config) != nil {
		return ""
	}
	adb := config.VM.NAT.PortForward.ADB
	port, err := strconv.Atoi(strings.TrimSpace(adb.HostPort))
	if err != nil || port <= 0 || port > 65535 {
		return ""
	}
	host := strings.TrimSpace(adb.HostIP)
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	if !strings.HasPrefix(host, "127.") {
		return ""
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// muMuInstance names an instance like MuMu's multi-instance manager: the
// name in extra_config.json followed by the folder's trailing index.
func muMuInstance(folder string, extraConfig []byte) string {
	var extra struct {
		PlayerName string `json:"playerName"`
	}
	_ = json.Unmarshal(extraConfig, &extra)
	name := strings.TrimSpace(extra.PlayerName)
	index := ""
	if cut := strings.LastIndex(folder, "-"); cut >= 0 {
		if _, err := strconv.Atoi(folder[cut+1:]); err == nil {
			index = "#" + folder[cut+1:]
		}
	}
	switch {
	case name != "" && index != "":
		return name + " " + index
	case name != "":
		return name
	case index != "":
		return index
	}
	return folder
}

func muMuEndpoints() []emulatorEndpoint {
	roots := muMuInstallRoots()
	for _, variable := range []string{"ProgramFiles", "ProgramFiles(x86)"} {
		if base := os.Getenv(variable); base != "" {
			matches, _ := filepath.Glob(filepath.Join(base, "Netease", "MuMu*"))
			roots = append(roots, matches...)
		}
	}
	return muMuEndpointsIn(roots)
}

func muMuEndpointsIn(roots []string) []emulatorEndpoint {
	seenRoot := make(map[string]bool)
	seen := make(map[string]bool)
	var endpoints []emulatorEndpoint
	for _, root := range roots {
		key := strings.ToLower(filepath.Clean(root))
		if root == "" || seenRoot[key] {
			continue
		}
		seenRoot[key] = true
		configs, _ := filepath.Glob(filepath.Join(root, "vms", "*", "configs", "vm_config.json"))
		sort.Strings(configs)
		for _, path := range configs {
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			if endpoint := parseMuMuEndpoint(data); endpoint != "" && !seen[endpoint] {
				seen[endpoint] = true
				configs := filepath.Dir(path)
				extra, _ := os.ReadFile(filepath.Join(configs, "extra_config.json"))
				instance := muMuInstance(filepath.Base(filepath.Dir(configs)), extra)
				endpoints = append(endpoints, emulatorEndpoint{endpoint, emulatorMuMu, instance})
			}
		}
	}
	return endpoints
}

// LDPlayer records the actual ADB forwarding ports in its NAT service logs.
// Wildcard listeners also accept 127.0.0.2, avoiding MuMu's more specific
// 127.0.0.1 listener when both emulators use port 5555.
var ldPlayerForwarding = regexp.MustCompile(`(?m)Event: (Add|Delete) IPv4 port-forwarding rule "([^"]+)"(?:: TCP ([^\s:]*):([0-9]+) -> [0-9.]+:5555\b)?`)
var ldPlayerNATLog = regexp.MustCompile(`^LdNatNetwork[0-9]+\.log$`)

// ldPlayerInstance turns a forwarding rule name such as "tcp_5555_0" into
// the instance index LDPlayer's multi-instance manager shows.
func ldPlayerInstance(rule string) string {
	if cut := strings.LastIndex(rule, "_"); cut >= 0 {
		if _, err := strconv.Atoi(rule[cut+1:]); err == nil {
			return "#" + rule[cut+1:]
		}
	}
	return ""
}

func parseLDPlayerEndpoints(data []byte) []emulatorEndpoint {
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
	names := make([]string, 0, len(rules))
	for name := range rules {
		names = append(names, name)
	}
	sort.Strings(names)
	seen := make(map[string]bool)
	var endpoints []emulatorEndpoint
	for _, name := range names {
		if endpoint := rules[name]; !seen[endpoint] {
			seen[endpoint] = true
			endpoints = append(endpoints, emulatorEndpoint{endpoint, emulatorLDPlayer, ldPlayerInstance(name)})
		}
	}
	sort.Slice(endpoints, func(i, j int) bool { return endpoints[i].address < endpoints[j].address })
	return endpoints
}

func ldPlayerEndpoints() []emulatorEndpoint {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	endpoints := ldPlayerEndpointsIn(filepath.Join(home, ".Ld9VirtualBox"))
	nameLDPlayerInstances(endpoints, ldPlayerDataDirs())
	return endpoints
}

// nameLDPlayerInstances adds the name given to a renamed instance, kept in
// <DataDir>/config/leidian<N>.config. Unnamed instances keep their index.
func nameLDPlayerInstances(endpoints []emulatorEndpoint, dataDirs []string) {
	for index, endpoint := range endpoints {
		number := strings.TrimPrefix(endpoint.instance, "#")
		if number == endpoint.instance {
			continue
		}
		for _, dir := range dataDirs {
			data, err := os.ReadFile(filepath.Join(dir, "config", "leidian"+number+".config"))
			if err != nil {
				continue
			}
			var config struct {
				PlayerName string `json:"statusSettings.playerName"`
			}
			if json.Unmarshal(data, &config) == nil && strings.TrimSpace(config.PlayerName) != "" {
				endpoints[index].instance = strings.TrimSpace(config.PlayerName) + " " + endpoint.instance
			}
			break
		}
	}
}

func ldPlayerEndpointsIn(root string) []emulatorEndpoint {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	seen := make(map[string]bool)
	var endpoints []emulatorEndpoint
	for _, entry := range entries {
		if entry.IsDir() || !ldPlayerNATLog.MatchString(entry.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, entry.Name()))
		if err != nil {
			continue
		}
		for _, endpoint := range parseLDPlayerEndpoints(data) {
			if !seen[endpoint.address] {
				seen[endpoint.address] = true
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

var adbPortLine = regexp.MustCompile(`(?m)^bst\.instance\.([^.]+)\.(?:status\.)?adb_port="([0-9]+)"\s*$`)
var blueStacksDisplayName = regexp.MustCompile(`(?m)^bst\.instance\.([^.]+)\.display_name="([^"]*)"\s*$`)

func parseBlueStacksEndpoints(data []byte) []emulatorEndpoint {
	names := make(map[string]string)
	for _, match := range blueStacksDisplayName.FindAllSubmatch(data, -1) {
		names[string(match[1])] = strings.TrimSpace(string(match[2]))
	}
	instances := make(map[int]string)
	for _, match := range adbPortLine.FindAllSubmatch(data, -1) {
		port, err := strconv.Atoi(string(match[2]))
		if err != nil || port <= 0 || port > 65535 {
			continue
		}
		if _, seen := instances[port]; !seen {
			instance := string(match[1])
			if name := names[instance]; name != "" {
				instance = name
			}
			instances[port] = instance
		}
	}
	ports := make([]int, 0, len(instances))
	for port := range instances {
		ports = append(ports, port)
	}
	sort.Ints(ports)
	var endpoints []emulatorEndpoint
	for _, port := range ports {
		endpoints = append(endpoints, emulatorEndpoint{net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), emulatorBlueStacks, instances[port]})
	}
	return endpoints
}

func blueStacksEndpoints() []emulatorEndpoint {
	root := os.Getenv("ProgramData")
	if root == "" {
		return nil
	}
	var endpoints []emulatorEndpoint
	seen := make(map[string]bool)
	for _, folder := range []string{"BlueStacks_nxt", "BlueStacks"} {
		data, err := os.ReadFile(filepath.Join(root, folder, "bluestacks.conf"))
		if err != nil {
			continue
		}
		for _, endpoint := range parseBlueStacksEndpoints(data) {
			if !seen[endpoint.address] {
				endpoints = append(endpoints, endpoint)
				seen[endpoint.address] = true
			}
		}
	}
	return endpoints
}
