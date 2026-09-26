package control

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func newestInput(root string, names ...string) (time.Time, error) {
	var newest time.Time
	for _, name := range names {
		err := filepath.WalkDir(filepath.Join(root, name), func(p string, d fs.DirEntry, err error) error {
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if info.ModTime().After(newest) {
				newest = info.ModTime()
			}
			return nil
		})
		if err != nil {
			return newest, err
		}
	}
	return newest, nil
}

func needsBuild(filename string, newest time.Time) bool {
	info, err := os.Stat(filename)
	return err != nil || newest.After(info.ModTime())
}

func webDependenciesNeedInstall(webDir string) (bool, error) {
	lock, err := os.Stat(filepath.Join(webDir, "package-lock.json"))
	if err != nil {
		return false, err
	}
	installed, err := os.Stat(filepath.Join(webDir, "node_modules", ".package-lock.json"))
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	for _, command := range []string{"tsc.cmd", "vite.cmd"} {
		if _, err := os.Stat(filepath.Join(webDir, "node_modules", ".bin", command)); errors.Is(err, os.ErrNotExist) {
			return true, nil
		} else if err != nil {
			return false, err
		}
	}
	return lock.ModTime().After(installed.ModTime()), nil
}

func (r *NativeRunner) buildCommand(ctx context.Context, dir, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	hideWindow(cmd)
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, os.Stderr, os.Stderr
	cmd.Env = append(os.Environ(), "GOTELEMETRY=off")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build %s: %w", name, err)
	}
	return nil
}

// EnsureExecutables builds changed workspace sources before replacing binaries.
// Published executable paths outside this workspace stay immutable.
func (r *NativeRunner) EnsureExecutables(ctx context.Context) error {
	if _, err := os.Stat(filepath.Join(r.root, "go.mod")); errors.Is(err, os.ErrNotExist) {
		for _, executable := range []string{r.server, r.launcher} {
			if _, err := os.Stat(executable); err != nil {
				return fmt.Errorf("incomplete published installation, missing executable %s: %w", executable, err)
			}
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect source project: %w", err)
	}
	if err := r.ensureNativeExecutables(ctx); err != nil {
		return err
	}

	webTime, err := newestInput(r.root, "web/src", "web/package.json", "web/package-lock.json", "web/vite.config.ts", "web/tsconfig.json", "web/tsconfig.app.json", "web/index.html")
	if err != nil {
		return err
	}
	if needsBuild(filepath.Join(r.root, "internal/admin/dist/index.html"), webTime) {
		webDir := filepath.Join(r.root, "web")
		install, err := webDependenciesNeedInstall(webDir)
		if err != nil {
			return fmt.Errorf("inspect frontend dependencies: %w", err)
		}
		if install {
			if err := r.buildCommand(ctx, webDir, "npm.cmd", "ci"); err != nil {
				return fmt.Errorf("install frontend dependencies: %w", err)
			}
		}
		if err := r.buildCommand(ctx, webDir, "npm.cmd", "run", "build"); err != nil {
			return err
		}
	}
	newest, err := newestInput(r.root, "cmd", "internal", "go.mod", "go.sum")
	if err != nil {
		return err
	}
	for _, target := range []struct{ name, executable string }{{"server", r.server}, {"launcher", r.launcher}} {
		local := filepath.Join(r.root, "bin", "ptcgp-"+target.name+".exe")
		if filepath.Clean(target.executable) != filepath.Clean(local) || !needsBuild(target.executable, newest) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(local), 0700); err != nil {
			return err
		}
		candidate := target.executable + ".next.exe"
		if err := r.buildCommand(ctx, r.root, "go", "build", "-buildvcs=false", "-o", candidate, "./cmd/ptcgp-"+target.name); err != nil {
			return err
		}
		if err := r.stopProcess(target.name, target.executable); err != nil {
			return err
		}
		if err := replaceExecutable(ctx, candidate, target.executable); err != nil {
			return err
		}
	}
	return nil
}

var nativeExecutables = []string{"ptcgp-importer.exe", "ptcgp-reader-x86_64", "ptcgp-reader-aarch64"}

func nativeNeedsBuild(root string) (bool, error) {
	newest, err := newestInput(root, "native/build.ps1", "native/importer/src", "native/importer/Cargo.toml", "native/importer/Cargo.lock", "native/reader/src", "native/reader/Cargo.toml", "native/reader/Cargo.lock")
	if err != nil {
		return false, err
	}
	for _, name := range nativeExecutables {
		if needsBuild(filepath.Join(root, "bin", name), newest) {
			return true, nil
		}
	}
	return false, nil
}

func (r *NativeRunner) ensureNativeExecutables(ctx context.Context) error {
	// Installed signed releases are immutable, even inside a source checkout.
	if filepath.Clean(r.server) != filepath.Join(r.root, "bin", "ptcgp-server.exe") {
		return nil
	}
	needed, err := nativeNeedsBuild(r.root)
	if err != nil || !needed {
		return err
	}
	bin := filepath.Join(r.root, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(bin, ".native-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	fmt.Fprintln(os.Stderr, "Compilation de l’importeur Rust et des lecteurs Android…")
	if err := r.buildCommand(ctx, r.root, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", filepath.Join(r.root, "native", "build.ps1"), "-OutputDirectory", stage); err != nil {
		return fmt.Errorf("compile native tools (Rust and both musl targets are required): %w", err)
	}
	for _, name := range nativeExecutables {
		if _, err := os.Stat(filepath.Join(stage, name)); err != nil {
			return fmt.Errorf("native build did not produce %s: %w", name, err)
		}
	}
	for _, name := range nativeExecutables {
		target := filepath.Join(bin, name)
		if err := replaceExecutable(ctx, filepath.Join(stage, name), target); err != nil {
			return err
		}
		// Cargo may reuse an older artifact when only the build script changed.
		now := time.Now()
		if err := os.Chtimes(target, now, now); err != nil {
			return err
		}
	}
	return nil
}

func replaceExecutable(ctx context.Context, candidate, target string) error {
	// Windows can retain the image mapping briefly after the process exits.
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := os.Rename(candidate, target)
		if err == nil || !errors.Is(err, os.ErrPermission) || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
