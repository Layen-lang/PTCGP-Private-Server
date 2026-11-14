//go:build !windows

package android

func muMuInstallRoots() []string { return nil }

func ldPlayerDataDirs() []string { return nil }
