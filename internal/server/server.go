// Package server composes REST and gRPC behind one TLS HTTP/2 endpoint.
package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/admin"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/android"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/assets"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/catalog"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/contracts"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/packlab"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/playerapi"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/protocol"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/restapi"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/traffic"
	"golang.org/x/net/http2"
)

// Config defines the local server runtime.
type Config struct {
	Client          protocol.Profile
	Contracts       contracts.Expectations
	Address         string
	AdminAddress    string
	DatabasePath    string
	Certificate     string
	PrivateKey      string
	MasterDataPath  string
	ImageDataPath   string
	TrafficLogPath  string
	StrictMetadata  bool
	ADBSerial       string
	AndroidPackage  string
	AndroidActivity string
}

// Run validates dependencies, serves until cancellation, and shuts down cleanly.
func Run(ctx context.Context, config Config, logger *slog.Logger) error {
	if _, err := contracts.VerifyProto(config.Contracts); err != nil {
		return err
	}
	catalogs, err := catalog.OpenRegistry(config.MasterDataPath, catalog.DefaultLocale, catalog.FallbackLocale)
	if err != nil {
		return err
	}
	catalogInfo := catalogs.Default()
	state, err := store.Open(ctx, config.DatabasePath)
	if err != nil {
		return err
	}
	defer state.Close()
	players := player.New(state, catalogInfo)
	packs := packlab.New(state, catalogInfo)
	trafficRecorder, err := traffic.Open(config.TrafficLogPath)
	if err != nil {
		return err
	}
	defer trafficRecorder.Close()
	images, imageErr := assets.Open(config.ImageDataPath)
	if imageErr != nil {
		logger.Warn("datamined images unavailable; administration will use text fallbacks", "error", imageErr)
	}
	grpcServer, err := playerapi.NewGRPCServer(players, packs, catalogInfo, logger, config.StrictMetadata, config.Client, trafficRecorder)
	if err != nil {
		return err
	}
	restHandler := trafficRecorder.HTTPMiddleware(restapi.New(state, logger, config.Client))
	if err := requireLoopback(config.AdminAddress); err != nil {
		return err
	}
	adminHandler, err := admin.New(players, android.New(config.ADBSerial, config.AndroidPackage, config.AndroidActivity), logger, admin.WithCatalogVersion(config.Client.AppVersion), admin.WithCatalogs(catalogs), admin.WithPackLab(packs), admin.WithImages(images), admin.WithTraffic(trafficRecorder))
	if err != nil {
		return err
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			grpcServer.ServeHTTP(w, r)
			return
		}
		restHandler.ServeHTTP(w, r)
	})
	httpServer := &http.Server{
		Addr:              config.Address,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			NextProtos: []string{"h2", "http/1.1"},
		},
	}
	if err := http2.ConfigureServer(httpServer, &http2.Server{}); err != nil {
		return fmt.Errorf("configure HTTP/2: %w", err)
	}
	adminServer := &http.Server{Addr: config.AdminAddress, Handler: adminHandler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second}
	errCh := make(chan error, 2)
	go func() {
		logger.Info("local server listening", "address", config.Address, "catalog_cards", len(catalogInfo.Cards()), "catalog_items", len(catalogInfo.Items()), "traffic_log", config.TrafficLogPath)
		errCh <- httpServer.ListenAndServeTLS(config.Certificate, config.PrivateKey)
	}()
	go func() {
		logger.Info("local administration listening", "address", config.AdminAddress)
		errCh <- adminServer.ListenAndServe()
	}()
	var runErr error
	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			runErr = err
		}
	}
	grpcServer.GracefulStop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tlsErr := httpServer.Shutdown(shutdownCtx)
	adminErr := adminServer.Shutdown(shutdownCtx)
	if runErr != nil {
		return fmt.Errorf("serve local endpoint: %w", runErr)
	}
	if tlsErr != nil {
		return fmt.Errorf("shutdown local TLS server: %w", tlsErr)
	}
	if adminErr != nil {
		return fmt.Errorf("shutdown local administration: %w", adminErr)
	}
	return nil
}

func requireLoopback(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid administration address %q: %w", address, err)
	}
	if host != "127.0.0.1" {
		return fmt.Errorf("administration must listen on 127.0.0.1, got %q", host)
	}
	return nil
}
