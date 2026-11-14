package android

import (
	"context"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Device struct {
	Serial  string   `json:"serial"`
	Name    string   `json:"name"`
	State   string   `json:"state"`
	Aliases []string `json:"aliases,omitempty"`
	// Kind is "emulator", "usb" or "wifi".
	Kind string `json:"kind,omitempty"`
	// Emulator and Instance name the emulator when it is recognised, for
	// example "MuMu" and "Android Device #1".
	Emulator string `json:"emulator,omitempty"`
	Instance string `json:"instance,omitempty"`
	// Model is the advertised model, which emulators borrow from real phones.
	Model string `json:"model,omitempty"`
}

// deviceIdentity is what listDevices learns from a connected device's shell.
type deviceIdentity struct {
	id, name, model, emulator string
	virtual                   bool
}

// identityCommand reads every property plus the user-visible device name in
// a single shell, asked once per boot.
const identityMarker = "--ptcgp-device-name--"
const identityCommand = "getprop; echo " + identityMarker + "; settings get global device_name"

var propertyLine = regexp.MustCompile(`^\[([^\]]+)\]: \[(.*)\]$`)

// parseIdentity names a device from its properties. Emulators copy a real
// phone's model, so they are told apart by their x86 processor and by
// properties or services each emulator adds to its system image.
func parseIdentity(output string) deviceIdentity {
	props, deviceName, _ := strings.Cut(output, identityMarker)
	values := make(map[string]string)
	var emulator string
	for _, line := range strings.Split(props, "\n") {
		match := propertyLine.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		key, value := match[1], strings.TrimSpace(match[2])
		values[key] = value
		text := strings.ToLower(key + "=" + value)
		switch {
		// Product values are borrowed from real phones and say nothing.
		case emulator != "" || strings.HasPrefix(key, "ro.product."):
		case strings.Contains(text, "ldinit") || strings.Contains(text, "com.android.ld.") || strings.Contains(text, "ldmnq"):
			emulator = emulatorLDPlayer
		case strings.Contains(text, "nemu") || strings.Contains(text, "mumu"):
			emulator = emulatorMuMu
		// BlueStacks runs services named bst* (bstfolder, bstsvcmgrtest).
		case strings.Contains(text, "bluestacks") || strings.HasPrefix(key, "init.svc.bst"):
			emulator = emulatorBlueStacks
		}
	}
	identity := deviceIdentity{model: values["ro.product.model"], emulator: emulator}
	identity.virtual = emulator != "" || strings.HasPrefix(values["ro.product.cpu.abi"], "x86") || values["ro.kernel.qemu"] == "1"
	identity.name = identity.model
	if identity.virtual {
		return identity
	}
	deviceName = strings.TrimSpace(deviceName)
	// Phones without a chosen name usually report their model here.
	if deviceName == "null" || deviceName == identity.model || strings.ContainsAny(deviceName, "\n:") {
		deviceName = ""
	}
	for _, name := range []string{deviceName, values["ro.product.marketname"], values["ro.product.vendor.marketname"], values["ro.config.marketing_name"]} {
		if name != "" {
			identity.name = name
			break
		}
	}
	return identity
}

// identityCache remembers each running Android's model by boot ID. The boot
// ID itself is asked on every listing: ADB reconnects a dropped TCP device
// under the same transport_id, so after an emulator stops, the same serial
// and transport may reach another emulator listening on that port.
type identityCache struct {
	mu      sync.Mutex
	entries map[string]deviceIdentity
}

var deviceIdentities = &identityCache{}

// Devices includes unauthorized and offline devices so the panel can explain
// why an emulator cannot be selected. Local forwarding endpoints are refreshed
// even while another emulator remains connected.
func Devices(ctx context.Context, runner Runner) ([]Device, error) {
	return listDevices(ctx, runner, localEmulatorEndpoints(), deviceIdentities)
}

type adbListing struct {
	serial, state, transport string
}

func readDevices(ctx context.Context, runner Runner) ([]adbListing, error) {
	data, err := runner.Run(ctx, "adb", "devices", "-l")
	if err != nil {
		return nil, err
	}
	var result []adbListing
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] == "List" {
			continue
		}
		entry := adbListing{serial: f[0], state: f[1]}
		for _, field := range f[2:] {
			if value, ok := strings.CutPrefix(field, "transport_id:"); ok {
				entry.transport = value
			}
		}
		result = append(result, entry)
	}
	return result, nil
}

func listDevices(ctx context.Context, runner Runner, endpoints []emulatorEndpoint, cache *identityCache) ([]Device, error) {
	listing, err := readDevices(ctx, runner)
	if err != nil {
		return nil, err
	}
	online := make(map[string]bool, len(listing))
	for _, entry := range listing {
		online[entry.serial] = entry.state == "device"
	}
	// Probe only the local endpoints ADB is not already connected to: this
	// finds emulators started meanwhile without spawning ADB for the others.
	connected := false
	for _, source := range endpoints {
		endpoint := source.address
		if online[endpoint] {
			continue
		}
		probeCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		// Stopped emulators normally refuse the connection. Continue listing
		// the other devices after this bounded probe.
		out, probeErr := runner.Run(probeCtx, "adb", "connect", endpoint)
		cancel()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		text := strings.ToLower(string(out))
		if probeErr == nil && !strings.Contains(text, "cannot") && !strings.Contains(text, "failed") && !strings.Contains(text, "unable") {
			connected = true
		}
	}
	if connected {
		if listing, err = readDevices(ctx, runner); err != nil {
			return nil, err
		}
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	known := cache.entries
	cache.entries = make(map[string]deviceIdentity, len(listing))
	result := []Device{}
	identities := []deviceIdentity{}
	seen := map[string]int{}
	for _, entry := range listing {
		serial := entry.serial
		key := serial
		identity := deviceIdentity{name: serial}
		if entry.state == "device" {
			out, e := runner.Run(ctx, "adb", "-s", serial, "shell", "cat /proc/sys/kernel/random/boot_id")
			if bootID := strings.TrimSpace(string(out)); e == nil && bootID != "" {
				key = bootID
				cached, ok := cache.entries[bootID]
				if !ok {
					cached, ok = known[bootID]
				}
				if !ok {
					cached = deviceIdentity{name: serial}
					if out, e := runner.Run(ctx, "adb", "-s", serial, "shell", identityCommand); e == nil {
						if parsed := parseIdentity(string(out)); parsed.model != "" {
							cached = parsed
						}
					}
					cached.id = bootID
				}
				// A device still booting is asked its model again next time.
				if cached.model != "" {
					cache.entries[bootID] = cached
				}
				identity = cached
			}
		}
		if index, exists := seen[key]; exists {
			if len(result[index].Aliases) == 0 {
				result[index].Aliases = []string{result[index].Serial}
			}
			result[index].Aliases = append(result[index].Aliases, serial)
			continue
		}
		seen[key] = len(result)
		result = append(result, Device{Serial: serial, Name: identity.name, State: entry.state, Model: identity.model})
		identities = append(identities, identity)
	}
	sources := make(map[string]emulatorEndpoint, len(endpoints))
	for _, endpoint := range endpoints {
		sources[endpoint.address] = endpoint
	}
	for index := range result {
		describeDevice(&result[index], identities[index], sources)
	}
	return result, nil
}

// describeDevice tells emulators from phones and names the emulator
// instance. Several emulators may claim the same local port (BlueStacks and
// LDPlayer both default to 5555), so the emulator recognised over ADB picks
// which configuration applies; without it, the configurations must agree.
func describeDevice(device *Device, identity deviceIdentity, sources map[string]emulatorEndpoint) {
	serials := device.Aliases
	if len(serials) == 0 {
		serials = []string{device.Serial}
	}
	var matches []emulatorEndpoint
	local, usb := false, false
	for _, serial := range serials {
		if source, ok := sources[serial]; ok {
			matches = append(matches, source)
		}
		switch host, _, err := net.SplitHostPort(serial); {
		case strings.HasPrefix(serial, "emulator-"):
			local = true
		case err == nil:
			if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() || host == "localhost" {
				local = true
			}
		case !strings.Contains(serial, "._adb-tls-"):
			usb = true
		}
	}
	emulator := identity.emulator
	if emulator == "" {
		for _, match := range matches {
			if emulator == "" {
				emulator = match.emulator
			} else if emulator != match.emulator {
				emulator = ""
				break
			}
		}
	}
	for _, match := range matches {
		if match.emulator == emulator && match.instance != "" {
			device.Instance = match.instance
			break
		}
	}
	device.Emulator = emulator
	switch {
	case identity.virtual || emulator != "" || local:
		device.Kind = "emulator"
	case usb:
		device.Kind = "usb"
	default:
		device.Kind = "wifi"
	}
}
