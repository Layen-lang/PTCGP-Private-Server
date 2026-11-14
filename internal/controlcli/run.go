// Package controlcli implements the short-lived command-line controller used
// by the Windows launcher.
package controlcli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/configuration"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/control"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
)

// Run manages the local panel, game server, and Android routing.
func Run(args []string) error {
	// Accept case-insensitive Windows-style flag names (-Serial, -Json…).
	for i, arg := range args {
		if strings.HasPrefix(arg, "-") && !strings.Contains(arg, "=") {
			args[i] = strings.ToLower(arg)
		}
	}
	configPath, args, err := configuration.Arguments(args)
	if err != nil {
		return err
	}
	action := "ui"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action, args = args[0], args[1:]
	}
	flags := flag.NewFlagSet("ptcgp-server "+action, flag.ContinueOnError)
	serial := flags.String("serial", os.Getenv("PTCGP_ADB_SERIAL"), "ADB serial (automatic when empty)")
	packageName := flags.String("package", "", "Android package override")
	noOpen := flags.Bool("noopen", false, "do not open the desktop window")
	jsonOutput := flags.Bool("json", false, "print machine-readable status")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("arguments inattendus: %v", flags.Args())
	}
	if action != "ui" && action != "local" && action != "online" && action != "stop" && action != "status" && action != "verify" && action != "apply" && action != "rollback" {
		return fmt.Errorf("action inconnue %q", action)
	}
	cfg, err := configuration.Load(configPath)
	if err != nil {
		return err
	}
	if *serial != "" {
		cfg.Android.Serial = *serial
	}
	if *packageName != "" {
		cfg.Android.Package = *packageName
	}
	root := cfg.DataRoot()
	runner, err := control.NewNativeRunner(cfg, root)
	if err != nil {
		return err
	}
	journal, err := control.NewJournal(cfg.Runtime.RuntimeDirectory)
	if err != nil {
		return err
	}
	runner.SetJournal(journal)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if action == "ui" {
		return runner.StartPanel(ctx, !*noOpen)
	}
	if action == "verify" || action == "apply" || action == "rollback" {
		return runner.SetupAndroid(ctx, action)
	}
	var status control.Status
	// A running panel hosts the game servers: act through it.
	if panelStatus, ok, panelErr := panelAction(ctx, cfg.Runtime.LauncherAddress, action); ok {
		if panelErr != nil {
			return panelErr
		}
		return printStatus(panelStatus, *jsonOutput)
	}
	state, err := store.Open(ctx, cfg.Runtime.Database)
	if err != nil {
		return err
	}
	defer state.Close()
	devices, err := control.NewMultiRunner(cfg, root, state)
	if err != nil {
		return err
	}
	devices.SetJournal(journal)
	if action == "status" {
		status, err = devices.Status(ctx)
	} else {
		status, err = devices.Action(ctx, control.Action(action))
	}
	if err != nil {
		return err
	}
	if action == "stop" {
		if err := runner.StopPanel(); err != nil {
			return err
		}
	}
	if action == "local" || action == "online" {
		if err := runner.StartPanel(ctx, !*noOpen); err != nil {
			return err
		}
	}
	return printStatus(status, *jsonOutput)
}

func printStatus(status control.Status, jsonOutput bool) error {
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(status)
	}
	fmt.Printf("Serveur : %t\nAndroid : %t (%s)\nRoot : %t\nMode : %s\nRoutage : %s\nCertificats : %s\nBibliothèque TLS : %s\nJeu : %s\n", status.Server.Running, status.Android.Connected, status.Android.Serial, status.Android.Root, status.Mode, status.Android.Routing, status.Android.CA, status.Android.Native, status.Android.Game)
	return nil
}
