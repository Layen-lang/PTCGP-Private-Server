//go:build windows

package android

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

// muMuInstallRoots reads MuMu's install directories from the uninstall
// entries, so installations outside Program Files are found too.
func muMuInstallRoots() []string {
	var roots []string
	for _, location := range []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`},
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`},
		{registry.CURRENT_USER, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`},
	} {
		parent, err := registry.OpenKey(location.root, location.path, registry.ENUMERATE_SUB_KEYS)
		if err != nil {
			continue
		}
		names, _ := parent.ReadSubKeyNames(-1)
		parent.Close()
		for _, name := range names {
			if !strings.Contains(strings.ToLower(name), "mumu") {
				continue
			}
			key, err := registry.OpenKey(location.root, location.path+`\`+name, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			if value, _, err := key.GetStringValue("InstallLocation"); err == nil && strings.TrimSpace(value) != "" {
				roots = append(roots, strings.Trim(strings.TrimSpace(value), `"`))
			}
			key.Close()
		}
	}
	return roots
}

// ldPlayerDataDirs reads where each installed LDPlayer keeps its instance
// configurations (the vms folder).
func ldPlayerDataDirs() []string {
	parent, err := registry.OpenKey(registry.CURRENT_USER, `Software\XuanZhi`, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil
	}
	names, _ := parent.ReadSubKeyNames(-1)
	parent.Close()
	var dirs []string
	for _, name := range names {
		key, err := registry.OpenKey(registry.CURRENT_USER, `Software\XuanZhi\`+name, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		if value, _, err := key.GetStringValue("DataDir"); err == nil && strings.TrimSpace(value) != "" {
			dirs = append(dirs, strings.Trim(strings.TrimSpace(value), `"`))
		}
		key.Close()
	}
	return dirs
}
