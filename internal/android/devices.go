package android

import (
	"context"
	"strings"
	"time"
)

type Device struct {
	Serial  string   `json:"serial"`
	Name    string   `json:"name"`
	State   string   `json:"state"`
	Aliases []string `json:"aliases,omitempty"`
}

// Devices includes unauthorized and offline devices so the panel can explain
// why an emulator cannot be selected. Local forwarding endpoints are refreshed
// even while another emulator remains connected.
func Devices(ctx context.Context, runner Runner) ([]Device, error) {
	return listDevices(ctx, runner, localEmulatorEndpoints())
}

func listDevices(ctx context.Context, runner Runner, endpoints []string) ([]Device, error) {
	for _, endpoint := range endpoints {
		probeCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		// Stopped emulators normally refuse the connection. Continue listing
		// the other devices after this bounded probe.
		_, _ = runner.Run(probeCtx, "adb", "connect", endpoint)
		cancel()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	data, err := runner.Run(ctx, "adb", "devices", "-l")
	if err != nil {
		return nil, err
	}
	result := []Device{}
	seen := map[string]int{}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] == "List" {
			continue
		}
		serial := f[0]
		identity := serial
		name := serial
		if f[1] == "device" {
			id, e := runner.Run(ctx, "adb", "-s", serial, "shell", "cat /proc/sys/kernel/random/boot_id")
			if e == nil && strings.TrimSpace(string(id)) != "" {
				identity = strings.TrimSpace(string(id))
			}
			model, e := runner.Run(ctx, "adb", "-s", serial, "shell", "getprop ro.product.model")
			if e == nil && strings.TrimSpace(string(model)) != "" {
				name = strings.TrimSpace(string(model))
			}
		}
		if index, exists := seen[identity]; exists {
			if len(result[index].Aliases) == 0 {
				result[index].Aliases = []string{result[index].Serial}
			}
			result[index].Aliases = append(result[index].Aliases, serial)
			continue
		}
		seen[identity] = len(result)
		result = append(result, Device{Serial: serial, Name: name, State: f[1]})
	}
	return result, nil
}
