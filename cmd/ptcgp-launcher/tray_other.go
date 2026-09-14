//go:build !windows

package main

func startSystemTray(trayActions) (func(), error) {
	return func() {}, nil
}
