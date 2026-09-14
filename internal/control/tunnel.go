package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const fallbackPort = "14443"

type tunnelState struct {
	Serial string `json:"serial"`
	UID    int    `json:"uid"`
	Rule   string `json:"rule,omitempty"`
}

func (r *NativeRunner) tunnelFile() string {
	return filepath.Join(r.cfg.Runtime.RuntimeDirectory, "adb-tunnel.json")
}

func ownerTunnelRule(uid int) string {
	return "OUTPUT -d 127.0.0.1/32 -p tcp --dport 443 -m owner --uid-owner " + strconv.Itoa(uid) + " -m comment --comment PTCGP-LOCAL -j REDIRECT --to-ports " + fallbackPort
}

func loopbackTunnelRule() string {
	return "OUTPUT -d 127.0.0.1/32 -p tcp --dport 443 -m comment --comment PTCGP-LOCAL -j REDIRECT --to-ports " + fallbackPort
}

func tunnelRule(state tunnelState) string {
	if state.Rule == "loopback" {
		return loopbackTunnelRule()
	}
	// An empty value is the legacy owner-scoped state format.
	return ownerTunnelRule(state.UID)
}

func (r *NativeRunner) configureTunnel(ctx context.Context, serial, port, packageDetails string) error {
	if err := r.removeFallbackTunnel(ctx, serial); err != nil {
		return err
	}
	if _, err := r.adb(ctx, serial, "reverse", "tcp:443", "tcp:"+port); err == nil {
		return nil
	} else if !strings.Contains(strings.ToLower(err.Error()), "permission denied") {
		return err
	}
	// Some emulators allow su but keep adbd unprivileged. Forward a high port
	// and redirect only this package's loopback HTTPS sockets to that port.
	match := regexp.MustCompile(`\buserId=(\d+)\b`).FindStringSubmatch(packageDetails)
	if len(match) != 2 {
		return fmt.Errorf("Android UID not found for the fallback tunnel")
	}
	uid, err := strconv.Atoi(match[1])
	if err != nil || uid < 10000 {
		return fmt.Errorf("invalid Android UID for the fallback tunnel")
	}
	list, err := r.adb(ctx, serial, "reverse", "--list")
	if err != nil {
		return err
	}
	if strings.Contains(list, "tcp:"+fallbackPort+" ") {
		return fmt.Errorf("ADB port %s is already in use", fallbackPort)
	}
	if _, err := r.adb(ctx, serial, "reverse", "tcp:"+fallbackPort, "tcp:"+port); err != nil {
		return err
	}
	state := tunnelState{Serial: serial, UID: uid, Rule: "owner"}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.WriteFile(r.tunnelFile(), data, 0600); err != nil {
		_, cleanup := r.adb(ctx, serial, "reverse", "--remove", "tcp:"+fallbackPort)
		return errors.Join(err, cleanup)
	}
	if _, err := r.shell(ctx, serial, "iptables -t nat -A "+tunnelRule(state)); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "owner") {
			return errors.Join(err, r.removeFallbackTunnel(ctx, serial))
		}
		// Some legacy iptables builds have no owner match. The hosts override is
		// system-wide already, so keep this fallback confined to loopback:443.
		state.Rule = "loopback"
		data, marshalErr := json.Marshal(state)
		if marshalErr != nil {
			return errors.Join(marshalErr, r.removeFallbackTunnel(ctx, serial))
		}
		if writeErr := os.WriteFile(r.tunnelFile(), data, 0600); writeErr != nil {
			return errors.Join(writeErr, r.removeFallbackTunnel(ctx, serial))
		}
		if _, fallbackErr := r.shell(ctx, serial, "iptables -t nat -A "+tunnelRule(state)); fallbackErr != nil {
			return errors.Join(fallbackErr, r.removeFallbackTunnel(ctx, serial))
		}
		r.step("local", "Fallback ADB tunnel: HTTPS redirection limited to loopback")
		return nil
	}
	r.step("local", "Fallback ADB tunnel: HTTPS redirection limited to the game")
	return nil
}

func (r *NativeRunner) removeFallbackTunnel(ctx context.Context, serial string) error {
	data, err := os.ReadFile(r.tunnelFile())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var state tunnelState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	if state.Serial != serial || state.UID < 10000 {
		return fmt.Errorf("the saved tunnel belongs to another device or has an invalid UID")
	}
	rule := tunnelRule(state)
	if _, err := r.shell(ctx, serial, "if iptables -t nat -C "+rule+" 2>/dev/null; then iptables -t nat -D "+rule+"; fi"); err != nil {
		return err
	}
	list, err := r.adb(ctx, serial, "reverse", "--list")
	if err != nil {
		return err
	}
	if strings.Contains(list, "tcp:"+fallbackPort+" ") {
		if _, err := r.adb(ctx, serial, "reverse", "--remove", "tcp:"+fallbackPort); err != nil {
			return err
		}
	}
	return os.Remove(r.tunnelFile())
}
