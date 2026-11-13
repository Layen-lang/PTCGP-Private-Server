//go:build windows

package main

import (
	"os"
	"testing"
)

func TestSystemTrayStartsAndStops(t *testing.T) {
	icon, err := os.ReadFile("app-icon.svg")
	if err != nil {
		t.Fatal(err)
	}
	noop := func() {}
	stop, err := startSystemTray(trayActions{IconSVG: icon, Open: noop, Local: noop, Online: noop, Stop: noop})
	if err != nil {
		t.Skipf("System Tray unavailable in this Windows session: %v", err)
	}
	stop()
}
