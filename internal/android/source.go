package android

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/configuration"
)

// SystemRunner uses the same hidden ADB process as the Android controller.
func SystemRunner() Runner { return commandRunner{} }

type Source struct {
	Serial  string   `json:"serial"`
	Version string   `json:"version"`
	ABI     string   `json:"abi"`
	Root    string   `json:"root"`
	Library string   `json:"library"`
	UseSU   bool     `json:"useSU"`
	APKs    []string `json:"apks"`
}

func ShellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }

// InstalledVersion also works for an unsupported game, allowing the updater to
// find its matching server before local mode is enabled.
func InstalledVersion(ctx context.Context, runner Runner, cfg configuration.Config) (string, error) {
	serial, err := Discover(ctx, runner, cfg.Android.Serial, cfg.Android.Package)
	if err != nil {
		return "", err
	}
	out, err := runner.Run(ctx, "adb", "-s", serial, "shell", "dumpsys package "+ShellQuote(cfg.Android.Package))
	if err != nil {
		return "", err
	}
	match := regexp.MustCompile(`\bversionName=([^\s]+)`).FindSubmatch(out)
	if len(match) != 2 {
		return "", fmt.Errorf("game version unavailable")
	}
	return string(match[1]), nil
}

// Inspect is read-only. It checks the installed game before any server starts
// or routing changes, including when the caller uses the command line.
func Inspect(ctx context.Context, runner Runner, cfg configuration.Config) (Source, error) {
	var s Source
	serial, err := Discover(ctx, runner, cfg.Android.Serial, cfg.Android.Package)
	if err != nil {
		return s, err
	}
	s.Serial = serial
	call := func(command string) (string, error) {
		if s.UseSU {
			command = "su -c " + ShellQuote(command)
		}
		out, e := runner.Run(ctx, "adb", "-s", serial, "shell", command)
		return strings.TrimSpace(string(out)), e
	}
	uid, err := call("id -u")
	if err != nil || uid != "0" {
		s.UseSU = true
		uid, err = call("id -u")
	}
	if err != nil || uid != "0" {
		return s, fmt.Errorf("root access is required for local mode; enable root in your emulator")
	}
	details, err := call("dumpsys package " + ShellQuote(cfg.Android.Package))
	if err != nil {
		return s, err
	}
	m := regexp.MustCompile(`\bversionName=([^\s]+)`).FindStringSubmatch(details)
	if len(m) == 2 {
		s.Version = m[1]
	}
	if s.Version != cfg.Client.AppVersion {
		return s, fmt.Errorf("game %s is installed; this server supports %s", s.Version, cfg.Client.AppVersion)
	}
	apks, err := call("pm path " + ShellQuote(cfg.Android.Package))
	if err != nil {
		return s, err
	}
	for _, line := range strings.Split(apks, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "package:") {
			s.APKs = append(s.APKs, strings.TrimPrefix(strings.TrimSpace(line), "package:"))
		}
	}
	if len(s.APKs) == 0 {
		return s, fmt.Errorf("game APKs are unavailable")
	}
	native := regexp.MustCompile(`\bnativeLibraryDir=([^\s]+)`).FindStringSubmatch(details)
	if len(native) == 2 {
		s.Library = path.Join(native[1], cfg.Patch.Library)
	} else {
		s.Library = path.Join(path.Dir(s.APKs[0]), "lib/arm64", cfg.Patch.Library)
	}
	hash, err := call("sha256sum " + ShellQuote(s.Library))
	if err != nil {
		// Android can load native code directly from a split APK without
		// extracting it. Inspect the packaged bytes without modifying the device;
		// setupAndroid will materialize the library when applying the patch.
		if _, existsErr := call("test -f " + ShellQuote(s.Library)); existsErr == nil {
			return s, fmt.Errorf("game native library unavailable at %s: %w (%s)", s.Library, err, hash)
		}
		entry := "lib/arm64-v8a/" + cfg.Patch.Library
		found := false
		for _, apk := range s.APKs {
			if _, entryErr := call("unzip -l " + ShellQuote(apk) + " | grep -F -- " + ShellQuote(entry) + " >/dev/null 2>&1"); entryErr != nil {
				continue
			}
			hash, err = call("unzip -p " + ShellQuote(apk) + " " + ShellQuote(entry) + " | sha256sum")
			if err != nil {
				return s, fmt.Errorf("read game native library from %s: %w (%s)", apk, err, hash)
			}
			found = true
			break
		}
		if !found {
			return s, fmt.Errorf("game native library %s unavailable at %s and in installed APKs: %w", cfg.Patch.Library, s.Library, err)
		}
	}
	fields := strings.Fields(hash)
	if len(fields) == 0 || (fields[0] != cfg.Patch.SourceSHA256 && fields[0] != cfg.Patch.TargetSHA256) {
		return s, fmt.Errorf("game build does not match the validated compatibility profile")
	}
	// uname may report the translated game's architecture. Inspect a system ELF.
	elf, err := call("od -An -tu1 -j18 -N2 /system/bin/toybox")
	switch strings.Join(strings.Fields(elf), " ") {
	case "62 0":
		s.ABI = "x86_64"
	case "183 0":
		s.ABI = "aarch64"
	default:
		if err != nil {
			return s, err
		}
		return s, fmt.Errorf("unsupported emulator system architecture: %s", elf)
	}
	external, _ := call("printf %s \"$EXTERNAL_STORAGE\"")
	candidates := []string{path.Join(external, "Android/data", cfg.Android.Package, "files"), path.Join("/storage/emulated/0/Android/data", cfg.Android.Package, "files"), path.Join("/sdcard/Android/data", cfg.Android.Package, "files")}
	for _, candidate := range candidates {
		if !strings.HasPrefix(candidate, "/") {
			continue
		}
		if _, e := call("test -d " + ShellQuote(path.Join(candidate, "Sharin.Resources"))); e == nil {
			s.Root = candidate
			break
		}
	}
	if s.Root == "" {
		return s, fmt.Errorf("game resources are missing; finish downloading them in official mode")
	}
	for namespace, hash := range map[string]string{"aladin": cfg.Client.MasterMemoryAladdinHash, "Default": cfg.Client.AndroidAssetAladdinHash} {
		if len(hash) < 2 {
			return s, fmt.Errorf("invalid resource revision")
		}
		if _, err = call("test -r " + ShellQuote(path.Join(s.Root, "Sharin.Resources", namespace, "index", hash[:2], hash+".aladin"))); err != nil {
			return s, fmt.Errorf("required game data revision is missing; finish the official game download")
		}
	}
	return s, nil
}
