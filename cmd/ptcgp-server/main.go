// Command ptcgp-server is the single PTCGP Private Server backend: the
// persistent control panel API used by the desktop application (accounts,
// Android control, data preparation), every device's game server hosted in
// the same process, the command-line actions, and the server maintenance
// subcommands.
package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/admin"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/assets"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/catalog"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/configuration"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/control"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/controlcli"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/packlab"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/provision"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/servercmd"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
)

//go:embed app-icon.svg
var trayIcon []byte

func main() {
	// Server maintenance subcommands (serve, cert, patch, verify…).
	if _, rest, err := configuration.Arguments(append([]string(nil), os.Args[1:]...)); err == nil && len(rest) > 0 && servercmd.Commands[rest[0]] {
		servercmd.Main()
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "serve-panel" {
		runPanel(os.Args[2:])
		return
	}
	if err := controlcli.Run(os.Args[1:]); err != nil {
		fatal(err)
	}
}

func runPanel(args []string) {
	// Every device's game server runs inside this process.
	control.HostServer = servercmd.Serve
	defer control.StopHostedServers()
	configPath, args, err := configuration.Arguments(args)
	if err != nil {
		fatal(err)
	}
	cfg, err := configuration.Load(configPath)
	if err != nil {
		fatal(err)
	}
	flags := flag.NewFlagSet("ptcgp-server", flag.ExitOnError)
	address := flags.String("address", cfg.Runtime.LauncherAddress, "control panel address")
	backendAddress := flags.String("backend", "http://"+cfg.Runtime.AdminAddress, "private server administration URL")
	projectRoot := flags.String("project-root", cfg.DataRoot(), "data directory")
	flags.Parse(args)
	backend, err := url.Parse(*backendAddress)
	if err != nil {
		fatal(err)
	}
	version := backupBeforeUpgrade(cfg)
	state, err := store.Open(context.Background(), cfg.Runtime.Database)
	if err != nil {
		fatal(err)
	}
	defer state.Close()
	runner, err := control.NewMultiRunner(cfg, *projectRoot, state)
	if err != nil {
		fatal(err)
	}
	handler, err := control.New(backend, runner)
	if err != nil {
		fatal(err)
	}
	journal, err := control.NewJournal(cfg.Runtime.RuntimeDirectory)
	if err != nil {
		fatal(err)
	}
	runner.SetJournal(journal)

	handler.SetAdministration(nil, journal, cfg.Android.Package, cfg.Android.Activity)
	program := cfg.ProgramDir()
	preparation := provision.New(cfg, *projectRoot, program)
	handler.SetVersion(version)
	defer handler.ClosePreparation()
	activatePrepared := func(prepared configuration.Config) error {
		catalogs, err := catalog.OpenRegistry(prepared.Data.MasterData, catalog.DefaultLocale, catalog.FallbackLocale)
		if err != nil {
			return err
		}
		images, err := assets.Open(prepared.Data.Images)
		if err != nil {
			return err
		}
		master := catalogs.Default()
		administration, err := admin.New(player.New(state, master), handler, slog.Default(), admin.WithCatalogVersion(prepared.Client.AppVersion), admin.WithCatalogs(catalogs), admin.WithPackLab(packlab.New(state, master)), admin.WithImages(images), admin.WithDeviceTrafficHistory(handler.TrafficHistory))
		if err != nil {
			return err
		}
		handler.SetAdministration(administration, journal, prepared.Android.Package, prepared.Android.Activity)
		runner.SetPreparedConfig(prepared)
		return nil
	}
	handler.SetPreparation(preparation, func(ctx context.Context, serial string) error {
		prepared, err := preparation.Prepare(ctx, serial)
		if err != nil {
			return err
		}
		if err := activatePrepared(prepared); err != nil {
			return err
		}
		return provision.WriteJSON(filepath.Join(*projectRoot, "data", "device.json"), map[string]string{"serial": prepared.Android.Serial})
	})

	server := &http.Server{Addr: *address, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second}
	shutdownRequested := make(chan struct{}, 1)
	handler.SetShutdown(func() {
		select {
		case shutdownRequested <- struct{}{}:
		default:
		}
	})
	defer removeOwnedPID(filepath.Join(cfg.Runtime.RuntimeDirectory, "launcher.pid"))
	slog.Info("control panel available", "address", "http://"+*address)
	journal.Record("panel", "success", "Administration available at http://"+*address)
	serveResult := make(chan error, 2)
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		fatal(err)
	}
	if err = os.MkdirAll(cfg.Runtime.RuntimeDirectory, 0700); err != nil {
		listener.Close()
		fatal(err)
	}
	if err = os.WriteFile(filepath.Join(cfg.Runtime.RuntimeDirectory, "launcher.pid"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		listener.Close()
		fatal(err)
	}
	go func() { serveResult <- server.Serve(listener) }()
	// Validate and load the existing data generation only after the panel
	// listens: clients can connect immediately and observe the "prepare"
	// operation instead of waiting for catalog loading.
	handler.StartRestore(func() bool {
		prepared, restoreErr := preparation.Restore()
		if restoreErr != nil {
			return false
		}
		if err := activatePrepared(prepared); err != nil {
			preparation.Fail(err)
			journal.Record("prepare", "error", err.Error())
			return false
		}
		return true
	})
	runTrayAction := func(action control.Action) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if err := requestTrayAction(ctx, *address, action); err != nil {
			journal.Record("panel", "error", "System Tray action: "+err.Error())
		}
	}
	stopTray, trayErr := startSystemTray(trayActions{
		IconSVG: trayIcon,
		Open: func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if err := runner.StartPanel(ctx, true); err != nil {
				journal.Record("panel", "error", "Opening from the System Tray: "+err.Error())
			}
		},
		Local:  func() { runTrayAction(control.ActionLocal) },
		Online: func() { runTrayAction(control.ActionOnline) },
		Stop:   func() { runTrayAction(control.ActionStop) },
	})
	if trayErr != nil {
		journal.Record("panel", "warning", "System Tray unavailable: "+trayErr.Error())
	} else {
		defer stopTray()
	}
	select {
	case err := <-serveResult:
		if err != nil && err != http.ErrServerClosed {
			fatal(err)
		}
	case <-shutdownRequested:
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := server.Shutdown(ctx)
		cancel()
		if err != nil {
			journal.Record("panel", "error", "Stopping the control panel: "+err.Error())
			fmt.Fprintln(os.Stderr, err)
			return
		}
		if err := <-serveResult; err != nil && err != http.ErrServerClosed {
			fatal(err)
		}
	}
}

func removeOwnedPID(path string) {
	value, err := os.ReadFile(path)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(value)))
	if err == nil && pid == os.Getpid() {
		_ = os.Remove(path)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

// backupBeforeUpgrade copies the accounts the first time a new program
// version starts, before it can migrate the database. One backup is kept. It
// returns the installed version.
func backupBeforeUpgrade(cfg configuration.Config) string {
	data, err := os.ReadFile(filepath.Join(cfg.ProgramDir(), "VERSION"))
	version := strings.TrimSpace(string(data))
	if err != nil || version == "" {
		return ""
	}
	marker := filepath.Join(cfg.DataRoot(), "data", "version")
	if previous, _ := os.ReadFile(marker); strings.TrimSpace(string(previous)) == version {
		return version
	}
	if _, err = os.Stat(cfg.Runtime.Database); err == nil {
		backups := filepath.Join(cfg.DataRoot(), "data", "backups")
		if err = os.MkdirAll(backups, 0700); err == nil {
			old, _ := filepath.Glob(filepath.Join(backups, "accounts-before-*.db*"))
			for _, path := range old {
				os.Remove(path)
			}
			target := filepath.Join(backups, "accounts-before-"+version+".db")
			for _, suffix := range []string{"", "-wal"} {
				var content []byte
				if content, err = os.ReadFile(cfg.Runtime.Database + suffix); err == nil {
					err = os.WriteFile(target+suffix, content, 0600)
				} else if suffix != "" && os.IsNotExist(err) {
					err = nil
				}
				if err != nil {
					break
				}
			}
		}
		if err != nil {
			// Without a backup, try again at the next start.
			slog.Warn("accounts were not backed up before the upgrade", "error", err)
			return version
		}
	}
	if err = os.MkdirAll(filepath.Dir(marker), 0700); err == nil {
		err = os.WriteFile(marker, []byte(version), 0600)
	}
	if err != nil {
		slog.Warn("cannot record the installed version", "error", err)
	}
	return version
}
