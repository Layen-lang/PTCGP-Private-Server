// Package android contains the small ADB adapter used by the local
// administration. It never modifies routing, TLS, or the APK.
package android

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

const localHostsMarker = "# PTCGP-LOCAL-BEGIN"

type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type commandRunner struct{}

func (commandRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	hideWindow(cmd)
	return cmd.CombinedOutput()
}

type Controller struct {
	serial, packageName, activity string
	runner                        Runner
}

func New(serial, packageName, activity string) *Controller {
	return NewWithRunner(serial, packageName, activity, commandRunner{})
}
func NewWithRunner(serial, packageName, activity string, runner Runner) *Controller {
	return &Controller{serial: serial, packageName: packageName, activity: activity, runner: runner}
}

// Open verifies that Android is connected and currently routed to this
// local server before restarting UnityPlayerActivity.
func (c *Controller) Open(ctx context.Context) error {
	if c.runner == nil {
		return fmt.Errorf("ADB runner is unavailable")
	}
	if c.serial == "" {
		serial, err := c.discover(ctx)
		if err != nil {
			return err
		}
		selected := *c
		selected.serial = serial
		return selected.Open(ctx)
	}
	state, err := c.adb(ctx, "get-state")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(state)) != "device" {
		return fmt.Errorf("Android %s is not connected", c.serial)
	}
	hosts, err := c.adb(ctx, "shell", "cat", "/system/etc/hosts")
	if err != nil {
		return fmt.Errorf("verify local routing: %w", err)
	}
	if !strings.Contains(string(hosts), localHostsMarker) {
		return fmt.Errorf("server mode is not local: Android hosts marker is absent")
	}
	if _, err := c.adb(ctx, "shell", "am", "force-stop", c.packageName); err != nil {
		return fmt.Errorf("stop Pokémon TCG Pocket: %w", err)
	}
	component := c.packageName + "/" + c.activity
	output, err := c.adb(ctx, "shell", "am", "start", "-n", component)
	if err != nil {
		return fmt.Errorf("start UnityPlayerActivity: %w", err)
	}
	if strings.Contains(string(output), "Error:") || strings.Contains(string(output), "Error type") {
		return fmt.Errorf("start game: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

func (c *Controller) discover(ctx context.Context) (string, error) {
	return Discover(ctx, c.runner, "", c.packageName)
}

func (c *Controller) adb(ctx context.Context, args ...string) ([]byte, error) {
	all := append([]string{"-s", c.serial}, args...)
	output, err := c.runner.Run(ctx, "adb", all...)
	if err != nil {
		return nil, fmt.Errorf("adb %s failed: %s: %w", strings.Join(args, " "), strings.TrimSpace(string(output)), err)
	}
	return output, nil
}
