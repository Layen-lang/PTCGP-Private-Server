package android

import (
	"context"
	"strings"
	"sync"
	"time"
)

type Device struct {
	Serial  string   `json:"serial"`
	Name    string   `json:"name"`
	State   string   `json:"state"`
	Aliases []string `json:"aliases,omitempty"`
}

// deviceIdentity is what listDevices learns from a connected device's shell.
type deviceIdentity struct{ id, name string }

// identityCache remembers each connection's boot ID and model. ADB assigns a
// new transport_id to every connection, so a key that includes it can never
// describe another emulator later reachable through the same serial.
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

func listDevices(ctx context.Context, runner Runner, endpoints []string, cache *identityCache) ([]Device, error) {
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
	for _, endpoint := range endpoints {
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
	seen := map[string]int{}
	for _, entry := range listing {
		serial := entry.serial
		identity := serial
		name := serial
		if entry.state == "device" {
			key := ""
			if entry.transport != "" {
				key = serial + "|" + entry.transport
			}
			cached, ok := known[key]
			if !ok || key == "" {
				cached = deviceIdentity{id: serial, name: serial}
				id, e := runner.Run(ctx, "adb", "-s", serial, "shell", "cat /proc/sys/kernel/random/boot_id")
				if e == nil && strings.TrimSpace(string(id)) != "" {
					cached.id = strings.TrimSpace(string(id))
				}
				model, e := runner.Run(ctx, "adb", "-s", serial, "shell", "getprop ro.product.model")
				if e == nil && strings.TrimSpace(string(model)) != "" {
					cached.name = strings.TrimSpace(string(model))
				}
				// Only fully answered probes are remembered; a device still
				// booting is asked again on the next listing.
				ok = cached.id != serial && cached.name != serial
			}
			if ok && key != "" {
				cache.entries[key] = cached
			}
			identity, name = cached.id, cached.name
		}
		if index, exists := seen[identity]; exists {
			if len(result[index].Aliases) == 0 {
				result[index].Aliases = []string{result[index].Serial}
			}
			result[index].Aliases = append(result[index].Aliases, serial)
			continue
		}
		seen[identity] = len(result)
		result = append(result, Device{Serial: serial, Name: name, State: entry.state})
	}
	return result, nil
}
