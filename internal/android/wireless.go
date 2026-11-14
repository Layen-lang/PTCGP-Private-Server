package android

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// Wireless debugging (Android 11 or later): the phone shows a pairing
// address and code once, then a connection address whose port changes each
// time wireless debugging is turned on.

// WirelessAddress validates "host:port" as typed from the phone's screen. It
// is passed to ADB as an argument, never through a shell.
func WirelessAddress(value string) (string, error) {
	value = strings.TrimSpace(value)
	host, port, err := net.SplitHostPort(value)
	if err != nil || host == "" || strings.HasPrefix(host, "-") || strings.ContainsAny(host, " \t/\\") {
		return "", fmt.Errorf("enter the address shown by the phone, for example 192.168.1.50:37099")
	}
	number, err := strconv.Atoi(port)
	if err != nil || number <= 0 || number > 65535 {
		return "", fmt.Errorf("the port after \":\" must be a number between 1 and 65535")
	}
	return net.JoinHostPort(host, strconv.Itoa(number)), nil
}

// WirelessPair pairs this computer with the phone, then connects to it when
// the phone announces its connection port on the network (mDNS). The result
// is the connected serial, or "" when the connection address must be typed.
func WirelessPair(ctx context.Context, runner Runner, address, code string) (string, error) {
	address, err := WirelessAddress(address)
	if err != nil {
		return "", err
	}
	code = strings.TrimSpace(code)
	if len(code) != 6 || strings.Trim(code, "0123456789") != "" {
		return "", fmt.Errorf("the pairing code has 6 digits")
	}
	pairCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	out, err := runner.Run(pairCtx, "adb", "pair", address, code)
	cancel()
	text := strings.TrimSpace(string(out))
	if err != nil || !strings.Contains(strings.ToLower(text), "successfully paired") {
		return "", fmt.Errorf("pairing failed: %s", adbMessage(text, err))
	}
	host, _, _ := net.SplitHostPort(address)
	// The phone announces its connection port shortly after pairing.
	for attempt := 0; attempt < 4; attempt++ {
		if endpoint := announcedEndpoint(ctx, runner, host); endpoint != "" {
			if serial, err := WirelessConnect(ctx, runner, endpoint); err == nil {
				return serial, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", nil
		case <-time.After(time.Second):
		}
	}
	return "", nil
}

// WirelessConnect connects to the address of the phone's Wireless debugging
// screen and returns the ADB serial.
func WirelessConnect(ctx context.Context, runner Runner, address string) (string, error) {
	address, err := WirelessAddress(address)
	if err != nil {
		return "", err
	}
	connectCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	out, err := runner.Run(connectCtx, "adb", "connect", address)
	cancel()
	text := strings.TrimSpace(string(out))
	lower := strings.ToLower(text)
	if err != nil || !strings.Contains(lower, "connected to") || strings.Contains(lower, "cannot") || strings.Contains(lower, "failed") {
		return "", fmt.Errorf("connection failed: %s", adbMessage(text, err))
	}
	return address, nil
}

// announcedEndpoint finds the connection service a paired phone advertises.
func announcedEndpoint(ctx context.Context, runner Runner, host string) string {
	listCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := runner.Run(listCtx, "adb", "mdns", "services")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || !strings.HasPrefix(fields[1], "_adb-tls-connect.") {
			continue
		}
		if announced, _, err := net.SplitHostPort(fields[2]); err == nil && announced == host {
			return fields[2]
		}
	}
	return ""
}

func adbMessage(text string, err error) string {
	if text != "" {
		return text
	}
	if err != nil {
		return err.Error()
	}
	return "no answer from ADB"
}
