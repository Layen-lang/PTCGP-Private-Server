// Command ptcgp-server runs and prepares the local PTCGP endpoint.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/assets"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/binarypatch"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/catalog"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/configuration"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/contracts"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/localtls"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/server"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(os.Args[1:], logger); err != nil {
		logger.Error("command failed", "error", err)
		os.Exit(1)
	}
}

func run(args []string, logger *slog.Logger) error {
	path, args, err := configuration.Arguments(args)
	if err != nil {
		return err
	}
	cfg, err := configuration.Load(path)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return fmt.Errorf("usage: ptcgp-server <serve|cert|verify|patch|probe>")
	}
	switch args[0] {
	case "inspect-proto":
		summary, err := contracts.InspectProto(contracts.DeclarationOrderV1)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(summary)
	case "serve":
		return runServe(args[1:], logger, cfg)
	case "cert":
		return runCert(args[1:], cfg)
	case "verify":
		summary, err := contracts.VerifyProto(cfg.Contracts)
		if err != nil {
			return err
		}
		if _, err := catalog.Open(cfg.Data.MasterData); err != nil {
			return err
		}
		if _, err := assets.Open(cfg.Data.Images); err != nil {
			return err
		}
		fmt.Printf("Player API: %d services, %d RPC, %d files, descriptor SHA-256 %s\n", summary.Services, summary.Methods, summary.Files, summary.DescriptorSHA256)
		return nil
	case "config":
		return json.NewEncoder(os.Stdout).Encode(cfg)
	case "patch":
		return runPatch(args[1:], cfg)
	case "probe":
		return runProbe(args[1:], cfg)
	case "asset-probe":
		return runAssetProbe(args[1:], cfg)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runPatch(args []string, cfg configuration.Config) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ptcgp-server patch <verify|apply|rollback> [flags]")
	}
	flags := flag.NewFlagSet("patch "+args[0], flag.ContinueOnError)
	target := flags.String("target", "", "native library to inspect or modify")
	backup := flags.String("backup", "", "backup path; defaults to TARGET.original")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *target == "" {
		return fmt.Errorf("--target is required")
	}
	if *backup == "" {
		*backup = *target + ".original"
	}
	manifest := cfg.Patch
	switch args[0] {
	case "verify":
		state, err := binarypatch.Verify(*target, manifest)
		if err != nil {
			return err
		}
		fmt.Printf("%s: %s (%s)\n", *target, state, manifest.Version)
		return nil
	case "apply":
		state, err := binarypatch.Apply(*target, *backup, manifest)
		if err != nil {
			return err
		}
		fmt.Printf("%s: %s; backup %s\n", *target, state, *backup)
		return nil
	case "rollback":
		if err := binarypatch.Rollback(*target, *backup, manifest); err != nil {
			return err
		}
		fmt.Printf("%s: restored from %s\n", *target, *backup)
		return nil
	default:
		return fmt.Errorf("unknown patch action %q", args[0])
	}
}

func runServe(args []string, logger *slog.Logger, cfg configuration.Config) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	address := flags.String("address", cfg.Runtime.Address, "TLS listen address")
	adminAddress := flags.String("admin-address", cfg.Runtime.AdminAddress, "loopback administration address")
	database := flags.String("database", cfg.Runtime.Database, "SQLite database")
	certificate := flags.String("cert", cfg.Runtime.Certificate, "server certificate")
	privateKey := flags.String("key", cfg.Runtime.PrivateKey, "server private key")
	masterData := flags.String("master-data", cfg.Data.MasterData, "master-data directory")
	imageData := flags.String("image-data", cfg.Data.Images, "read-only datamined image directory")
	trafficLog := flags.String("traffic-log", cfg.Runtime.TrafficLog, "sanitized request/response JSONL log")
	strict := flags.Bool("strict-metadata", cfg.Runtime.StrictMetadata, "require the configured Takasho metadata")
	adbSerial := flags.String("adb-serial", cfg.Android.Serial, "Android ADB serial")
	androidPackage := flags.String("android-package", cfg.Android.Package, "Android package")
	androidActivity := flags.String("android-activity", cfg.Android.Activity, "Android activity")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if _, err := contracts.VerifyProto(cfg.Contracts); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*database), 0o700); err != nil {
		return fmt.Errorf("create database directory: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return server.Run(ctx, server.Config{
		Client: cfg.Client, Contracts: cfg.Contracts,
		Address:         *address,
		AdminAddress:    *adminAddress,
		DatabasePath:    *database,
		Certificate:     *certificate,
		PrivateKey:      *privateKey,
		MasterDataPath:  *masterData,
		ImageDataPath:   *imageData,
		TrafficLogPath:  *trafficLog,
		StrictMetadata:  *strict,
		ADBSerial:       *adbSerial,
		AndroidPackage:  *androidPackage,
		AndroidActivity: *androidActivity,
	}, logger)
}

func runCert(args []string, cfg configuration.Config) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ptcgp-server cert <init|android-name>")
	}
	if args[0] == "android-name" {
		flags := flag.NewFlagSet("cert android-name", flag.ContinueOnError)
		ca := flags.String("ca", cfg.Runtime.CertificateAuthority, "CA certificate")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		name, err := localtls.AndroidSystemName(*ca)
		if err != nil {
			return err
		}
		fmt.Println(name)
		return nil
	}
	if args[0] != "init" && args[0] != "ensure" {
		return fmt.Errorf("usage: ptcgp-server cert <init|ensure|android-name>")
	}
	flags := flag.NewFlagSet("cert init", flag.ContinueOnError)
	directory := flags.String("directory", "", "optional output directory; defaults to configured certificate paths")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	var err error
	if args[0] == "ensure" {
		if *directory != "" {
			return fmt.Errorf("cert ensure uses configured paths")
		}
		err = localtls.EnsureFiles(localtls.Paths{CA: cfg.Runtime.CertificateAuthority, CAKey: filepath.Join(filepath.Dir(cfg.Runtime.CertificateAuthority), "ca-key.pem"), Certificate: cfg.Runtime.Certificate, PrivateKey: cfg.Runtime.PrivateKey}, cfg.Android.RedirectHosts)
	} else if *directory != "" {
		err = localtls.Generate(*directory, cfg.Android.RedirectHosts)
	} else {
		err = localtls.GenerateFiles(localtls.Paths{CA: cfg.Runtime.CertificateAuthority, CAKey: filepath.Join(filepath.Dir(cfg.Runtime.CertificateAuthority), "ca-key.pem"), Certificate: cfg.Runtime.Certificate, PrivateKey: cfg.Runtime.PrivateKey}, cfg.Android.RedirectHosts)
	}
	if err != nil {
		return err
	}
	fmt.Println("Local CA and server certificate ready")
	return nil
}
