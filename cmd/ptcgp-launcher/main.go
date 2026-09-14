// Command ptcgp-launcher serves the local web control panel while the private
// game server is switched between local and official modes.
package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
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
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "serve-panel" {
		runPanel(os.Args[2:])
		return
	}
	if err := controlcli.Run(os.Args[1:]); err != nil {
		fatal(err)
	}
}

func runPanel(args []string) {
	configPath, args, err := configuration.Arguments(args)
	if err != nil {
		fatal(err)
	}
	cfg, err := configuration.Load(configPath)
	if err != nil {
		fatal(err)
	}
	flags := flag.NewFlagSet("ptcgp-launcher", flag.ExitOnError)
	address := flags.String("address", cfg.Runtime.LauncherAddress, "control panel address")
	backendAddress := flags.String("backend", "http://"+cfg.Runtime.AdminAddress, "private server administration URL")
	projectRoot := flags.String("project-root", filepath.Dir(cfg.Path), "project directory")
	flags.Parse(args)
	backend, err := url.Parse(*backendAddress)
	if err != nil {
		fatal(err)
	}
	frontend, err := admin.Frontend()
	if err != nil {
		fatal(err)
	}
	trayIcon, err := fs.ReadFile(frontend, "app-icon.svg")
	if err != nil {
		fatal(err)
	}
	runner, err := control.NewNativeRunner(cfg, *projectRoot)
	if err != nil {
		fatal(err)
	}
	handler, err := control.New(frontend, backend, runner)
	if err != nil {
		fatal(err)
	}
	journal, err := control.NewJournal(cfg.Runtime.RuntimeDirectory)
	if err != nil {
		fatal(err)
	}
	runner.SetJournal(journal)
	journal.Record("panel", "info", "Starting administration and loading data")
	catalogs, err := catalog.OpenRegistry(cfg.Data.MasterData, catalog.DefaultLocale, catalog.FallbackLocale)
	if err != nil {
		journal.Record("panel", "error", err.Error())
		fatal(err)
	}
	master := catalogs.Default()
	state, err := store.Open(context.Background(), cfg.Runtime.Database)
	if err != nil {
		journal.Record("panel", "error", err.Error())
		fatal(err)
	}
	defer state.Close()
	images, err := assets.Open(cfg.Data.Images)
	if err != nil {
		journal.Record("panel", "warning", "Images unavailable: "+err.Error())
	}
	administration, err := admin.New(player.New(state, master), handler, slog.Default(),
		admin.WithCatalogVersion(cfg.Client.AppVersion), admin.WithCatalogs(catalogs), admin.WithPackLab(packlab.New(state, master)),
		admin.WithImages(images), admin.WithTrafficHistory(cfg.Runtime.TrafficLog))
	if err != nil {
		journal.Record("panel", "error", err.Error())
		fatal(err)
	}
	handler.SetAdministration(administration, journal, cfg.Android.Package, cfg.Android.Activity)
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
	serveResult := make(chan error, 1)
	go func() { serveResult <- server.ListenAndServe() }()
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
