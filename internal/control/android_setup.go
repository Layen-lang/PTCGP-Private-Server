package control

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/binarypatch"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/localtls"
)

const remoteBackupDir = "/data/local/tmp/ptcgp-private-server"
const remoteCADir = "/data/local/tmp/ptcgp-system-cacerts"

func fileSHA256(filename string) (string, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

// SetupAndroid exposes the former verify/apply/rollback maintenance commands.
func (r *NativeRunner) SetupAndroid(ctx context.Context, action string) error {
	if action != "verify" && action != "apply" && action != "rollback" {
		return fmt.Errorf("unknown Android action %q", action)
	}
	serial, err := r.discover(ctx)
	if err != nil {
		return err
	}
	if err := r.setupAndroid(ctx, serial, action); err != nil {
		return err
	}
	if action == "rollback" {
		return r.clearPendingRollback()
	}
	return nil
}

func (r *NativeRunner) writeLibrary(ctx context.Context, serial, source, target string) error {
	command := "cat " + shellQuote(source) + " > " + shellQuote(target)
	if _, err := r.shell(ctx, serial, command); err == nil {
		return nil
	}
	_, err := r.adb(ctx, serial, "shell", "su system sh -c "+shellQuote(command))
	return err
}

func (r *NativeRunner) checkHash(ctx context.Context, serial, filename, expected string) error {
	hash, err := r.remoteHash(ctx, serial, filename)
	if err != nil {
		return err
	}
	if hash != expected {
		return fmt.Errorf("unexpected SHA-256 for %s: %s", filename, hash)
	}
	return nil
}

// ensureLibraryPath materializes native libraries that Android keeps inside a
// split APK. Some emulators extract them under lib/arm64 while others load
// them directly from split_config.arm64_v8a.apk.
func (r *NativeRunner) ensureLibraryPath(ctx context.Context, serial, library string) (string, error) {
	target, err := r.libraryPath(ctx, serial, library)
	if err != nil {
		return "", err
	}
	if _, err := r.shell(ctx, serial, "test -f "+shellQuote(target)); err == nil {
		return target, nil
	}

	out, err := r.adb(ctx, serial, "shell", "pm", "path", r.cfg.Android.Package)
	if err != nil {
		return "", err
	}
	entry := "lib/arm64-v8a/" + library
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "package:") {
			continue
		}
		apk := strings.TrimPrefix(strings.TrimSpace(line), "package:")
		if _, err := r.shell(ctx, serial, "unzip -l "+shellQuote(apk)+" | grep -F -- "+shellQuote(entry)+" >/dev/null 2>&1"); err != nil {
			continue
		}
		temporary := "/data/local/tmp/" + library + ".ptcgp-extracted"
		command := "unzip -p " + shellQuote(apk) + " " + shellQuote(entry) + " > " + shellQuote(temporary) +
			" && test -s " + shellQuote(temporary) +
			" && mkdir -p " + shellQuote(path.Dir(target)) +
			" && cat " + shellQuote(temporary) + " > " + shellQuote(target) +
			" && chown system:system " + shellQuote(target) +
			" && chmod 755 " + shellQuote(target) +
			" && (restorecon " + shellQuote(target) + " >/dev/null 2>&1 || true)" +
			"; result=$?; rm -f " + shellQuote(temporary) + "; exit $result"
		if _, err := r.shell(ctx, serial, command); err != nil {
			return "", err
		}
		return target, nil
	}
	return "", fmt.Errorf("native library %s is absent from the extracted directory and installed APKs", library)
}

// pushRootFile stages through shell-writable storage before copying into a
// root-owned directory. Some ADB daemons cannot push there directly.
func (r *NativeRunner) pushRootFile(ctx context.Context, serial, local, target, mode string) error {
	temporary := "/data/local/tmp/ptcgp-push-" + path.Base(target)
	if _, err := r.adb(ctx, serial, "push", local, temporary); err != nil {
		return err
	}
	command := "cp " + shellQuote(temporary) + " " + shellQuote(target) +
		" && chmod " + mode + " " + shellQuote(target) +
		"; result=$?; rm -f " + shellQuote(temporary) + "; exit $result"
	_, err := r.shell(ctx, serial, command)
	return err
}

func (r *NativeRunner) setupAndroid(ctx context.Context, serial, action string) error {
	uid, err := r.shell(ctx, serial, "id -u")
	if err != nil {
		return err
	}
	if uid != "0" {
		return fmt.Errorf("Android root access is required")
	}
	m, err := r.manifest(action == "rollback")
	if err != nil {
		return err
	}
	library, err := r.ensureLibraryPath(ctx, serial, m.Library)
	if err != nil {
		return err
	}
	work := filepath.Join(r.root, "data", "android-patch")
	if err := os.MkdirAll(work, 0700); err != nil {
		return err
	}
	local := filepath.Join(work, m.Library)
	if _, err := r.adb(ctx, serial, "pull", library, local); err != nil {
		return err
	}
	hash, err := fileSHA256(local)
	if err != nil {
		return err
	}
	officialUpgrade := false
	if action == "rollback" && hash != m.SourceSHA256 && hash != m.TargetSHA256 {
		active, err := r.manifest(false)
		if err != nil {
			return err
		}
		officialUpgrade = hash == active.SourceSHA256
	}
	if !officialUpgrade && hash != m.SourceSHA256 && hash != m.TargetSHA256 {
		return fmt.Errorf("unsupported native library: %s", hash)
	}
	if action == "verify" {
		_, err := binarypatch.Verify(local, m)
		return err
	}
	backup := remoteBackupDir + "/yaha-" + m.SourceSHA256 + ".original"
	if action == "rollback" {
		if hash == m.TargetSHA256 {
			if _, err := r.shell(ctx, serial, "test -e "+shellQuote(backup)); err != nil {
				backup = remoteBackupDir + "/yaha-" + m.Version + ".original"
			}
			// Verify before overwriting the installed library, including legacy backups.
			if err := r.checkHash(ctx, serial, backup, m.SourceSHA256); err != nil {
				return err
			}
		}
		if _, err := r.adb(ctx, serial, "shell", "am", "force-stop", r.cfg.Android.Package); err != nil {
			return err
		}
		if hash == m.TargetSHA256 {
			if err := r.writeLibrary(ctx, serial, backup, library); err != nil {
				return err
			}
			if err := r.checkHash(ctx, serial, library, m.SourceSHA256); err != nil {
				return err
			}
		}
		r.step("rollback", "Restoring official routing and certificates")
		for _, target := range []string{"/system/etc/hosts", "/system/etc/security/cacerts"} {
			// Absence of a bind mount is normal after an emulator restart.
			if _, err := r.shell(ctx, serial, "if grep -q ' "+target+" ' /proc/mounts; then umount "+shellQuote(target)+"; fi"); err != nil {
				return err
			}
		}
		reverses, err := r.adb(ctx, serial, "reverse", "--list")
		if err != nil {
			return err
		}
		if strings.Contains(reverses, "tcp:443 ") {
			if _, err := r.adb(ctx, serial, "reverse", "--remove", "tcp:443"); err != nil {
				return err
			}
		}
		if err := r.removeFallbackTunnel(ctx, serial); err != nil {
			return err
		}
		if err := os.Remove(r.installedPatch()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	r.step("local", "Checking the game version and TLS patch")
	details, err := r.adb(ctx, serial, "shell", "dumpsys", "package", r.cfg.Android.Package)
	if err != nil {
		return err
	}
	version := regexp.MustCompile(`\bversionName=([^\s]+)`).FindStringSubmatch(details)
	if len(version) != 2 || version[1] != m.Version {
		return fmt.Errorf("the installed version does not match the prepared patch")
	}
	if hash == m.TargetSHA256 {
		if _, err := r.shell(ctx, serial, "test -e "+shellQuote(backup)); err != nil {
			legacyBackup := remoteBackupDir + "/yaha-" + m.Version + ".original"
			if _, legacyErr := r.shell(ctx, serial, "test -e "+shellQuote(legacyBackup)); legacyErr != nil {
				return fmt.Errorf("the game library is already patched, but its original backup is missing; reinstall or update the game from its official source before enabling local mode again")
			}
			backup = legacyBackup
		}
		if err := r.checkHash(ctx, serial, backup, m.SourceSHA256); err != nil {
			return err
		}
		if err := r.saveInstalledPatch(); err != nil {
			return err
		}
	}
	caName, err := localtls.AndroidSystemName(r.cfg.Runtime.CertificateAuthority)
	if err != nil {
		return err
	}
	if _, err := r.adb(ctx, serial, "shell", "am", "force-stop", r.cfg.Android.Package); err != nil {
		return err
	}
	if hash == m.SourceSHA256 {
		if _, err := binarypatch.Apply(local, local+"."+m.SourceSHA256+".original", m); err != nil {
			return err
		}
		remote := "/data/local/tmp/" + m.Library + ".ptcgp"
		if _, err := r.adb(ctx, serial, "push", local, remote); err != nil {
			return err
		}
		if _, err := r.shell(ctx, serial, "mkdir -p "+shellQuote(remoteBackupDir)+" && (test -e "+shellQuote(backup)+" || cp "+shellQuote(library)+" "+shellQuote(backup)+")"); err != nil {
			return err
		}
		if err := r.checkHash(ctx, serial, backup, m.SourceSHA256); err != nil {
			return err
		}
		// Persist rollback metadata before the first remote native write.
		if err := r.saveInstalledPatch(); err != nil {
			return err
		}
		if err := r.writeLibrary(ctx, serial, remote, library); err != nil {
			return err
		}
		if err := r.checkHash(ctx, serial, library, m.TargetSHA256); err != nil {
			return err
		}
	}
	r.step("local", "Installing the local certificate")
	if _, err := r.shell(ctx, serial, "if grep -q ' /system/etc/security/cacerts ' /proc/mounts; then umount /system/etc/security/cacerts; fi"); err != nil {
		return err
	}
	if _, err := r.shell(ctx, serial, "mkdir -p "+shellQuote(remoteCADir)+" && cp -a /system/etc/security/cacerts/. "+shellQuote(remoteCADir+"/")); err != nil {
		return err
	}
	if err := r.pushRootFile(ctx, serial, r.cfg.Runtime.CertificateAuthority, remoteCADir+"/"+caName, "644"); err != nil {
		return err
	}
	if _, err := r.shell(ctx, serial, "chmod 644 "+shellQuote(remoteCADir+"/"+caName)+" && mount --bind "+shellQuote(remoteCADir)+" /system/etc/security/cacerts"); err != nil {
		return err
	}
	r.step("local", "Configuring the ADB tunnel and local routing")
	_, port, err := net.SplitHostPort(r.cfg.Runtime.Address)
	if err != nil {
		return err
	}
	if err := r.configureTunnel(ctx, serial, port, details); err != nil {
		return err
	}
	hosts, err := r.adb(ctx, serial, "shell", "cat", "/system/etc/hosts")
	if err != nil {
		return err
	}
	localHosts := filepath.Join(work, "hosts")
	if err := os.WriteFile(localHosts, []byte(localHostsText(hosts, r.cfg.Android.RedirectHosts)), 0600); err != nil {
		return err
	}
	if _, err := r.adb(ctx, serial, "push", localHosts, "/data/local/tmp/hosts.ptcgp"); err != nil {
		return err
	}
	if _, err := r.shell(ctx, serial, "chmod 644 /data/local/tmp/hosts.ptcgp && if grep -q ' /system/etc/hosts ' /proc/mounts; then umount /system/etc/hosts; fi"); err != nil {
		return err
	}
	_, err = r.shell(ctx, serial, "mount --bind /data/local/tmp/hosts.ptcgp /system/etc/hosts")
	return err
}

func (r *NativeRunner) saveInstalledPatch() error {
	data, err := json.MarshalIndent(r.cfg.Patch, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(r.cfg.Runtime.RuntimeDirectory, 0700); err != nil {
		return err
	}
	tmp := r.installedPatch() + ".next"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, r.installedPatch())
}

func localHostsText(hosts string, domains []string) string {
	clean := regexp.MustCompile(`(?ms)^# PTCGP-LOCAL-BEGIN\r?\n.*?^# PTCGP-LOCAL-END\r?\n?`).ReplaceAllString(hosts, "")
	var block strings.Builder
	block.WriteString(strings.TrimRight(clean, "\r\n") + "\n# PTCGP-LOCAL-BEGIN\n")
	for _, domain := range domains {
		block.WriteString("127.0.0.1 " + domain + "\n")
	}
	block.WriteString("# PTCGP-LOCAL-END\n")
	return block.String()
}
