// Package configuration loads the server configuration: the file published
// with the program, plus the optional local overrides kept with user data.
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
	RedirectHosts []string `json:"redirectHosts"`
}

type Data struct {
	// Resolve fills these from a validated local generation before serving.
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
	// Path is the configuration published with the program; its directory
	// holds the program files (bin, profiles, VERSION).
	Path string `json:"-"`
	// Root holds user data (data, certs, server.local.json). It survives
	// program updates and is the base of relative paths.
	Root string `json:"-"`
}

// LocalName is the optional file of local overrides in the data root. Any
// value it contains replaces the published one; everything else follows the
// installed release.
const LocalName = "server.local.json"

// Root returns the data root for a program directory. An installed program
// lives in "current" next to Update.exe and is replaced on every update, so
// user data stays in the parent directory. Elsewhere (a source checkout) the
// program directory is also the data root.
func Root(program string) string {
	if strings.EqualFold(filepath.Base(program), "current") {
		parent := filepath.Dir(program)
		if info, err := os.Stat(filepath.Join(parent, "Update.exe")); err == nil && info.Mode().IsRegular() {
			return parent
		}
	}
	return program
}

// DataRoot is Root, derived from the program directory for a configuration
// that was not read by Load.
func (c Config) DataRoot() string {
	if c.Root != "" {
		return c.Root
	}
	return Root(c.ProgramDir())
}

// ProgramDir is the directory of the installed program files.
func (c Config) ProgramDir() string { return filepath.Dir(c.Path) }

// overlay replaces published values with local ones, object by object.
func overlay(base, local map[string]any) {
	for key, value := range local {
		if next, ok := value.(map[string]any); ok {
			if current, ok := base[key].(map[string]any); ok {
				overlay(current, next)
				continue
			}
		}
		base[key] = value
	}
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

// Load rejects incomplete configuration and resolves paths relative to the
// data root.
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
	c.Root = Root(filepath.Dir(absolute))
	root := c.Root
	if data, err := os.ReadFile(filepath.Join(root, LocalName)); err == nil {
		var local, merged map[string]any
		if err = json.Unmarshal(data, &local); err != nil {
			return c, fmt.Errorf("decode %s: %w", LocalName, err)
		}
		published, err := os.ReadFile(absolute)
		if err != nil {
			return c, err
		}
		if err = json.Unmarshal(published, &merged); err != nil {
			return c, err
		}
		overlay(merged, local)
		if data, err = json.Marshal(merged); err != nil {
			return c, err
		}
		// Unknown local keys are ignored: a release may retire a setting.
		if err = json.Unmarshal(data, &c); err != nil {
			return c, fmt.Errorf("decode %s: %w", LocalName, err)
		}
	} else if !os.IsNotExist(err) {
		return c, err
	}
	if c.Android.Serial == "" {
		var selected struct{ Serial string }
		if readJSON(filepath.Join(root, "data", "device.json"), &selected, false) == nil {
			c.Android.Serial = selected.Serial
		}
	}
	if err := c.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate server config: %w", err)
	}
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
	for name, value := range map[string]string{"database": c.Runtime.Database, "certificate": c.Runtime.Certificate, "privateKey": c.Runtime.PrivateKey, "certificateAuthority": c.Runtime.CertificateAuthority, "trafficLog": c.Runtime.TrafficLog, "runtimeDirectory": c.Runtime.RuntimeDirectory, "android.package": c.Android.Package, "android.activity": c.Android.Activity} {
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
