package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/configuration"
	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

func runAssetProbe(args []string, cfg configuration.Config) error {
	flags := flag.NewFlagSet("asset-probe", flag.ContinueOnError)
	url := flags.String("url", cfg.Client.AssetBaseURL+"access-check.txt", "official asset URL")
	if err := flags.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, *url, nil)
	if err != nil {
		return fmt.Errorf("create asset request: %w", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("request asset without cookie: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusForbidden {
		return fmt.Errorf("official CDN requires authorization: HTTP 403")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("asset probe returned %s", response.Status)
	}
	fmt.Printf("Official CDN accepted a cookie-free request: %s\n", response.Status)
	return nil
}

func runProbe(args []string, cfg configuration.Config) error {
	flags := flag.NewFlagSet("probe", flag.ContinueOnError)
	address := flags.String("address", "127.0.0.1:443", "server address")
	serverName := flags.String("server-name", cfg.Client.PlayerAPIHost, "TLS server name")
	caPath := flags.String("ca", cfg.Runtime.CertificateAuthority, "local CA")
	device := flags.String("device-account", "local-probe-device", "synthetic device account")
	if err := flags.Parse(args); err != nil {
		return err
	}
	caPEM, err := os.ReadFile(*caPath)
	if err != nil {
		return fmt.Errorf("read probe CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return fmt.Errorf("probe CA is invalid")
	}
	connection, err := grpc.NewClient(*address,
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			MinVersion: tls.VersionTLS12,
			RootCAs:    roots,
			ServerName: *serverName,
		})),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(transport.NewCodec())),
	)
	if err != nil {
		return fmt.Errorf("create probe client: %w", err)
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	base := metadata.Pairs(
		"x-takasho-app-version", cfg.Client.AppVersion,
		"x-takasho-protocol-version", cfg.Client.SDKVersion,
		"x-takasho-build-version", cfg.Client.BuildHash,
		"x-takasho-sdk-version", cfg.Client.ClientSDKVersion,
	)
	ctx = metadata.NewOutgoingContext(ctx, base)
	system := api.NewSystemClient(connection)
	authorization, err := system.AuthorizeV1(ctx, &api.SystemAuthorizeV1_Types_Request{DeviceAccount: *device})
	if err != nil {
		return fmt.Errorf("probe AuthorizeV1: %w", err)
	}
	authed := base.Copy()
	authed.Set("x-takasho-session-token", authorization.GetSessionToken())
	ctx = metadata.NewOutgoingContext(ctx, authed)
	if _, err := system.LoginV1(ctx, &api.SystemLoginV1_Types_Request{}); err != nil {
		return fmt.Errorf("probe LoginV1: %w", err)
	}
	if _, err := api.NewPlayerSettingsClient(connection).GetInfoV1(ctx, &api.PlayerSettingGetInfoV1_Types_Request{}); err != nil {
		return fmt.Errorf("probe GetInfoV1: %w", err)
	}
	profile, err := api.NewPlayerProfileClient(connection).MyProfileV1(ctx, &api.MyProfileV1_Types_Request{})
	if err != nil {
		return fmt.Errorf("probe MyProfileV1: %w", err)
	}
	if _, err := api.NewPlayerResourcesClient(connection).SyncV1(ctx, &api.PlayerResourcesSyncV1_Types_Request{}); err != nil {
		return fmt.Errorf("probe SyncV1: %w", err)
	}
	if _, err := api.NewPackClient(connection).GetPackPowerV1(ctx, &api.PackGetPackPowerV1_Types_Request{}); err != nil {
		return fmt.Errorf("probe GetPackPowerV1: %w", err)
	}
	fmt.Printf("TLS/HTTP2/Takasho bootstrap OK: player %s (%s), CDN %s\n",
		authorization.GetPlayerId(), profile.GetProfile().GetProfileSpine().GetNickname(), authorization.GetAssetBaseUrl())
	return nil
}
