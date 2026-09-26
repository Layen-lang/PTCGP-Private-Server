package android

import (
	"context"
	"strings"
)

type Device struct {
	Serial string `json:"serial"`
	Name   string `json:"name"`
	State  string `json:"state"`
}

// Devices includes unauthorized and offline devices so the panel can explain
// why an emulator cannot be selected. Only local configured endpoints are used.
func Devices(ctx context.Context, runner Runner) ([]Device, error) {
	for _, endpoint := range blueStacksEndpoints() {
		_, _ = runner.Run(ctx, "adb", "connect", endpoint)
	}
	data, err := runner.Run(ctx, "adb", "devices", "-l")
	if err != nil {
		return nil, err
	}
	result := []Device{}
	seen := map[string]bool{}
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
		if seen[identity] {
			continue
		}
		seen[identity] = true
		result = append(result, Device{serial, name, f[1]})
	}
	return result, nil
}
