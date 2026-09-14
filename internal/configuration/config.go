// Package configuration loads the single local server configuration.
package configuration

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/binarypatch"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/contracts"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/protocol"
)

type Runtime struct {
	StrictMetadata       bool   `json:"strictMetadata"`
	Address              string `json:"address"`
	AdminAddress         string `json:"adminAddress"`
	LauncherAddress      string `json:"launcherAddress"`
	Database             string `json:"database"`
	Certificate          string `json:"certificate"`
	PrivateKey           string `json:"privateKey"`
	CertificateAuthority string `json:"certificateAuthority"`
	TrafficLog           string `json:"trafficLog"`
	RuntimeDirectory     string `json:"runtimeDirectory"`
}

type Android struct {
	Serial        string   `json:"serial"`
	Package       string   `json:"package"`
	Activity      string   `json:"activity"`
	ServerAddress string   `json:"serverAddress"`
	RedirectHosts []string `json:"redirectHosts"`
}

type Data struct {
	MasterData string `json:"masterData"`
	Images     string `json:"images"`
}

// Config is resolved once at the command boundary and passed by value.
type Config struct {
	SchemaVersion int                    `json:"schemaVersion"`
	Runtime       Runtime                `json:"runtime"`
	Android       Android                `json:"android"`
	Data          Data                   `json:"data"`
	Client        protocol.Profile       `json:"client"`
	Contracts     contracts.Expectations `json:"contracts"`
	Patch         binarypatch.Manifest   `json:"patch"`
	Path          string                 `json:"-"`
}

// DefaultPath is independent of the package test working directory.
func DefaultPath() string {
	if path := os.Getenv("PTCGP_SERVER_CONFIG"); path != "" {
		return path
	}
	cwd, _ := os.Getwd()
	for dir := cwd; dir != ""; dir = filepath.Dir(dir) {
		path := filepath.Join(dir, "server.json")
		if _, err := os.Stat(path); err == nil {
			return path
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	if exe, err := os.Executable(); err == nil {
		path := filepath.Join(filepath.Dir(filepath.Dir(exe)), "server.json")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return "server.json"
}

// Arguments extracts --config wherever it appears, for every subcommand.
func Arguments(args []string) (string, []string, error) {
	path := DefaultPath()
	var rest []string
	for i := 0; i < len(args); i++ {
		value := args[i]
		if value == "--config" || value == "-config" {
			i++
			if i == len(args) {
				return "", nil, fmt.Errorf("--config requires a file")
			}
			path = args[i]
		} else if strings.HasPrefix(value, "--config=") || strings.HasPrefix(value, "-config=") {
			path = strings.SplitN(value, "=", 2)[1]
		} else {
			rest = append(rest, value)
		}
	}
	return path, rest, nil
}

func readJSON(path string, target any, strict bool) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 128<<20))
	if strict {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("unexpected trailing JSON in %s", path)
	}
	return nil
}

// Load rejects incomplete configuration and resolves paths relative to the file.
func Load(path string) (Config, error) {
	var c Config
	absolute, err := filepath.Abs(path)
	if err != nil {
		return c, err
	}
	if err := readJSON(absolute, &c, true); err != nil {
		return c, err
	}
	c.Path = absolute
	if err := c.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate server config: %w", err)
	}
	root := filepath.Dir(absolute)
	for _, value := range []*string{&c.Runtime.Database, &c.Runtime.Certificate, &c.Runtime.PrivateKey, &c.Runtime.CertificateAuthority, &c.Runtime.TrafficLog, &c.Runtime.RuntimeDirectory, &c.Data.MasterData, &c.Data.Images} {
		if *value != "" && !filepath.IsAbs(*value) {
			*value = filepath.Join(root, filepath.FromSlash(*value))
		}
	}
	return c, nil
}

func (c Config) Validate() error {
	if c.SchemaVersion != 1 {
		return fmt.Errorf("schemaVersion must be 1")
	}
	for _, address := range []string{c.Runtime.Address, c.Runtime.AdminAddress, c.Runtime.LauncherAddress} {
		if _, _, err := net.SplitHostPort(address); err != nil {
			return fmt.Errorf("invalid listen address %q", address)
		}
	}
	for _, address := range []string{c.Runtime.AdminAddress, c.Runtime.LauncherAddress} {
		host, _, _ := net.SplitHostPort(address)
		if host != "127.0.0.1" {
			return fmt.Errorf("administration must listen on 127.0.0.1")
		}
	}
	if c.Runtime.AdminAddress == c.Runtime.LauncherAddress {
		return fmt.Errorf("admin and launcher addresses must differ")
	}
	for name, value := range map[string]string{"database": c.Runtime.Database, "certificate": c.Runtime.Certificate, "privateKey": c.Runtime.PrivateKey, "certificateAuthority": c.Runtime.CertificateAuthority, "trafficLog": c.Runtime.TrafficLog, "runtimeDirectory": c.Runtime.RuntimeDirectory, "masterData": c.Data.MasterData, "images": c.Data.Images, "android.package": c.Android.Package, "android.activity": c.Android.Activity, "android.serverAddress": c.Android.ServerAddress} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	p := c.Client
	patterns := [][2]string{
		{p.AppVersion, `^\d+\.\d+\.\d+$`}, {p.SDKVersion, `^v\d+\.\d+\.\d+$`},
		{p.ResponseProtocolVersion, `^v\d+\.\d+\.\d+$`}, {p.ClientSDKVersion, `^\d+\.\d+\.\d+$`},
		{p.BuildHash, `^[a-f0-9]{40}$`}, {p.MasterMemoryAladdinHash, `^[a-f0-9]{16}$`},
		{p.AndroidAssetAladdinHash, `^[a-f0-9]{16}$`}, {c.Contracts.DescriptorSHA256, `^[a-f0-9]{64}$`},
	}
	for _, pair := range patterns {
		if !regexp.MustCompile(pair[1]).MatchString(pair[0]) {
			return fmt.Errorf("invalid release value %q", pair[0])
		}
	}
	if p.BaaSSDKVersion == "" || p.PlayerAPIHost == "" || p.BaaSHost == "" {
		return fmt.Errorf("client SDK and hosts are required")
	}
	u, err := url.Parse(p.AssetBaseURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("invalid assetBaseURL")
	}
	if c.Contracts.Services <= 0 || c.Contracts.Methods <= 0 {
		return fmt.Errorf("approved proto expectations are required")
	}
	if err := c.Patch.Validate(); err != nil {
		return fmt.Errorf("invalid patch: %w", err)
	}
	if c.Patch.Version != c.Client.AppVersion {
		return fmt.Errorf("patch version and client version differ")
	}
	if len(c.Android.RedirectHosts) == 0 {
		return fmt.Errorf("redirectHosts are required")
	}
	for _, host := range c.Android.RedirectHosts {
		if !regexp.MustCompile(`^[a-zA-Z0-9.-]+$`).MatchString(host) {
			return fmt.Errorf("invalid redirect host %q", host)
		}
	}
	return nil
}
