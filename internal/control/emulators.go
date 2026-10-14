package control

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/android"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/configuration"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/provision"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
)

type emulatorRuntime struct {
	Serial       string `json:"serial"`
	Address      string `json:"address"`
	AdminAddress string `json:"adminAddress"`
	Legacy       bool   `json:"legacy,omitempty"`
}

type emulatorControl interface {
	Runner
	SetJournal(*Journal)
	SetPreparedConfig(configuration.Config)
	launch(context.Context) error
	running() bool
}

// MultiRunner owns independent server processes and Android state for each
// emulator. The Handler serializes its operations, including device selection.
type MultiRunner struct {
	base           *NativeRunner
	state          *store.Store
	selected       string
	runtimes       map[string]emulatorRuntime
	devices        map[string]emulatorControl
	newDevice      func(configuration.Config) (emulatorControl, error)
	routingMu      sync.RWMutex
	routingSerial  string
	routingBackend string
	histories      map[string]string
	now            func() time.Time
}

// NewMultiRunner restores per-device runtimes without importing game data or
// starting games. The first device retains the original server ports and files.
func NewMultiRunner(cfg configuration.Config, root string, state *store.Store) (*MultiRunner, error) {
	base, err := NewNativeRunner(cfg, root)
	if err != nil {
		return nil, err
	}
	m := &MultiRunner{base: base, state: state, selected: cfg.Android.Serial, runtimes: make(map[string]emulatorRuntime), devices: make(map[string]emulatorControl), now: time.Now}
	data, err := os.ReadFile(m.runtimeFile())
	if err == nil {
		var entries []emulatorRuntime
		if err := json.Unmarshal(data, &entries); err != nil {
			return nil, fmt.Errorf("read emulator runtimes: %w", err)
		}
		for _, entry := range entries {
			if entry.Serial == "" || !localRuntimeAddress(entry.Address) || !localRuntimeAddress(entry.AdminAddress) || entry.Address == entry.AdminAddress {
				return nil, fmt.Errorf("invalid saved emulator runtime")
			}
			if _, exists := m.runtimes[entry.Serial]; exists {
				return nil, fmt.Errorf("duplicate emulator runtime")
			}
			m.runtimes[entry.Serial] = entry
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	// Preserve ownership of a legacy fallback tunnel if a different emulator
	// was selected before upgrading to independent runtimes.
	if len(m.runtimes) == 0 {
		var tunnel tunnelState
		if data, err := os.ReadFile(base.tunnelFile()); err == nil && json.Unmarshal(data, &tunnel) == nil && tunnel.Serial != "" {
			if _, err := m.device(tunnel.Serial); err != nil {
				return nil, err
			}
		}
	}
	for serial := range m.runtimes {
		if _, err := m.device(serial); err != nil {
			return nil, err
		}
	}
	m.publishRouting()
	return m, nil
}

func localRuntimeAddress(address string) bool {
	host, port, err := net.SplitHostPort(address)
	value, portErr := strconv.Atoi(port)
	return err == nil && host == "127.0.0.1" && portErr == nil && value > 0 && value <= 65535
}

func (m *MultiRunner) runtimeFile() string {
	return filepath.Join(m.base.cfg.Runtime.RuntimeDirectory, "emulators.json")
}

func (m *MultiRunner) saveRuntimes() error {
	entries := make([]emulatorRuntime, 0, len(m.runtimes))
	for _, entry := range m.runtimes {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Serial < entries[j].Serial })
	return provision.WriteJSON(m.runtimeFile(), entries)
}

func freeRuntimeAddress() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		return "", err
	}
	return address, nil
}

func (m *MultiRunner) unusedAddress() (string, error) {
	for attempt := 0; attempt < 32; attempt++ {
		address, err := freeRuntimeAddress()
		if err != nil {
			return "", err
		}
		used := address == m.base.cfg.Runtime.Address || address == m.base.cfg.Runtime.AdminAddress
		for _, entry := range m.runtimes {
			used = used || address == entry.Address || address == entry.AdminAddress
		}
		if !used {
			return address, nil
		}
	}
	return "", fmt.Errorf("could not allocate an independent emulator port")
}

func (m *MultiRunner) device(serial string) (emulatorControl, error) {
	if device := m.devices[serial]; device != nil {
		return device, nil
	}
	entry, exists := m.runtimes[serial]
	if !exists {
		entry = emulatorRuntime{Serial: serial, Legacy: len(m.runtimes) == 0}
		if entry.Legacy {
			entry.Address, entry.AdminAddress = m.base.cfg.Runtime.Address, m.base.cfg.Runtime.AdminAddress
		} else {
			var err error
			entry.Address, err = m.unusedAddress()
			if err != nil {
				return nil, err
			}
			for entry.AdminAddress == "" || entry.AdminAddress == entry.Address {
				entry.AdminAddress, err = m.unusedAddress()
				if err != nil {
					return nil, err
				}
			}
		}
		m.runtimes[serial] = entry
		if err := m.saveRuntimes(); err != nil {
			delete(m.runtimes, serial)
			return nil, err
		}
	}
	cfg := m.deviceConfig(entry)
	if m.newDevice != nil {
		device, err := m.newDevice(cfg)
		if err != nil {
			return nil, err
		}
		m.devices[serial] = device
		m.publishRouting()
		return device, nil
	}
	device, err := NewNativeRunner(cfg, m.base.root)
	if err != nil {
		return nil, err
	}
	device.commands, device.journal, device.isolated = m.base.commands, m.base.journal, true
	device.cachedSerial = serial
	m.devices[serial] = device
	m.publishRouting()
	return device, nil
}

func (m *MultiRunner) deviceConfig(entry emulatorRuntime) configuration.Config {
	cfg := m.base.cfg
	cfg.Android.Serial = entry.Serial
	cfg.Runtime.Address, cfg.Runtime.AdminAddress = entry.Address, entry.AdminAddress
	if !entry.Legacy {
		cfg.Runtime.RuntimeDirectory = emulatorRuntimeDirectory(cfg.Runtime.RuntimeDirectory, entry.Serial)
		cfg.Runtime.TrafficLog = filepath.Join(cfg.Runtime.RuntimeDirectory, "traffic.jsonl")
	}
	return cfg
}

func emulatorRuntimeDirectory(root, serial string) string {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(serial)))
	return filepath.Join(root, "emulators", hash[:16])
}

// stopServersForBuild prevents sibling emulator processes from keeping a Windows
// server executable locked while the source launcher replaces it.
func (r *NativeRunner) stopServersForBuild() error {
	data, err := os.ReadFile(filepath.Join(r.cfg.Runtime.RuntimeDirectory, "emulators.json"))
	if errors.Is(err, os.ErrNotExist) {
		return r.stopProcess("server", r.server)
	}
	if err != nil {
		return err
	}
	var entries []emulatorRuntime
	if err := json.Unmarshal(data, &entries); err != nil {
		return err
	}
	var failures []error
	for _, entry := range entries {
		if entry.Serial == "" {
			return fmt.Errorf("invalid saved emulator runtime")
		}
		cfg := r.cfg
		if !entry.Legacy {
			cfg.Runtime.RuntimeDirectory = emulatorRuntimeDirectory(cfg.Runtime.RuntimeDirectory, entry.Serial)
		}
		device := &NativeRunner{cfg: cfg, isolated: true}
		if err := device.stopProcess("server", r.server); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// Routing is an immutable request snapshot, available while an action is busy.
func (m *MultiRunner) Routing() (string, string) {
	m.routingMu.RLock()
	defer m.routingMu.RUnlock()
	return m.routingSerial, m.routingBackend
}

func (m *MultiRunner) publishRouting() {
	histories := make(map[string]string, len(m.runtimes))
	backend := m.base.cfg.Runtime.AdminAddress
	for serial, entry := range m.runtimes {
		histories[serial] = m.deviceConfig(entry).Runtime.TrafficLog
		if serial == m.selected {
			backend = entry.AdminAddress
		}
	}
	m.routingMu.Lock()
	m.routingSerial, m.routingBackend, m.histories = m.selected, backend, histories
	m.routingMu.Unlock()
}

// TrafficHistory identifies the stored exchanges for one emulator.
func (m *MultiRunner) TrafficHistory(serial string) string {
	m.routingMu.RLock()
	defer m.routingMu.RUnlock()
	return m.histories[serial]
}

// SetJournal shares operation messages while keeping device runtime files apart.
func (m *MultiRunner) SetJournal(journal *Journal) {
	m.base.SetJournal(journal)
	for _, device := range m.devices {
		device.SetJournal(journal)
	}
}

// SetPreparedConfig makes the common imported data available to every device.
func (m *MultiRunner) SetPreparedConfig(cfg configuration.Config) {
	m.base.SetPreparedConfig(cfg)
	if m.selected == "" {
		m.selected = cfg.Android.Serial
	}
	for serial, device := range m.devices {
		device.SetPreparedConfig(m.deviceConfig(m.runtimes[serial]))
	}
	m.publishRouting()
}

// SelectDevice switches the control target without restarting any game or
// repeating installation preparation. Local mode checks compatibility later.
func (m *MultiRunner) SelectDevice(ctx context.Context, serial string) (Status, error) {
	serial = strings.TrimSpace(serial)
	if serial == "" {
		return Status{}, fmt.Errorf("choose a connected emulator")
	}
	resolved, err := android.Discover(ctx, m.base.commands, serial, m.base.cfg.Android.Package)
	if err != nil {
		return Status{}, err
	}
	// ADB can expose the same Android instance through several transports.
	// Reuse its running server and account reservation when selecting an alias.
	if _, exists := m.runtimes[resolved]; !exists {
		identity, identityErr := m.base.adb(ctx, resolved, "shell", "cat", "/proc/sys/kernel/random/boot_id")
		if identityErr == nil && identity != "" {
			for known := range m.runtimes {
				other, err := m.base.adb(ctx, known, "shell", "cat", "/proc/sys/kernel/random/boot_id")
				if err == nil && other == identity {
					resolved = known
					break
				}
			}
		}
	}
	device, err := m.device(resolved)
	if err != nil {
		return Status{}, err
	}
	s, err := device.Status(ctx)
	if err != nil {
		return Status{}, err
	}
	if err := provision.WriteJSON(filepath.Join(m.base.root, "data", "device.json"), map[string]string{"serial": resolved}); err != nil {
		return Status{}, err
	}
	m.selected = resolved
	m.publishRouting()
	s.Android.Serial = resolved
	return s, nil
}

// Status inspects only the selected device. Other runtimes remain untouched.
func (m *MultiRunner) Status(ctx context.Context) (Status, error) {
	if m.selected == "" {
		serial, err := m.base.discover(ctx)
		if err != nil {
			return m.base.Status(ctx)
		}
		m.selected = serial
		m.publishRouting()
	}
	device, err := m.device(m.selected)
	if err != nil {
		return Status{}, err
	}
	s, err := device.Status(ctx)
	// Keep the selected serial even while the emulator is disconnected.
	s.Android.Serial = m.selected
	if err == nil {
		owners, stateErr := m.state.EmulatorPlayers(ctx)
		if stateErr != nil {
			return s, stateErr
		}
		for _, serial := range owners {
			if managed := m.devices[serial]; managed != nil {
				stopped := !managed.running()
				if !stopped {
					profile, err := m.state.EmulatorProfile(ctx, serial)
					if err != nil {
						return s, err
					}
					// Allow Android to create the process after am start. Once it
					// has exited, the account can be opened on another emulator.
					if m.now().Sub(time.Unix(profile.StartedAt, 0)) > 15*time.Second {
						current := s
						var statusErr error
						if serial != m.selected {
							current, statusErr = managed.Status(ctx)
						}
						stopped = statusErr == nil && current.Android.Connected && !current.Android.Running
					}
				}
				if !stopped {
					continue
				}
				if err := m.state.ReleaseEmulator(ctx, serial); err != nil {
					return s, err
				}
			}
		}
		for serial, managed := range m.devices {
			if serial == m.selected {
				continue
			}
			pending := filepath.Join(m.deviceConfig(m.runtimes[serial]).Runtime.RuntimeDirectory, "pending-rollback")
			if _, pendingErr := os.Stat(pending); pendingErr == nil {
				recovery, recoveryErr := managed.Status(ctx)
				message := recovery.Android.RecoveryError
				if recoveryErr != nil {
					message = recoveryErr.Error()
				}
				if message != "" {
					s.Android.RecoveryError = strings.TrimSpace(s.Android.RecoveryError + " " + serial + ": " + message)
				}
			}
		}
	}
	return s, err
}

// Action applies Local/Official to one device and Stop to all managed devices.
func (m *MultiRunner) Action(ctx context.Context, action Action) (Status, error) {
	if action == ActionStop {
		return m.stopAll(ctx)
	}
	if m.selected == "" {
		if _, err := m.Status(ctx); err != nil {
			return Status{}, err
		}
	}
	if m.selected == "" {
		return Status{}, fmt.Errorf("choose an emulator first")
	}
	device, err := m.device(m.selected)
	if err != nil {
		return Status{}, err
	}
	s, err := device.Action(ctx, action)
	if action == ActionOnline && err == nil {
		err = m.state.ReleaseEmulator(ctx, m.selected)
	}
	return s, err
}

func (m *MultiRunner) stopAll(ctx context.Context) (Status, error) {
	if m.selected != "" {
		if _, err := m.device(m.selected); err != nil {
			return Status{}, err
		}
	}
	var failures []error
	for serial, device := range m.devices {
		_, err := device.Action(ctx, ActionStop)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", serial, err))
		}
		// A stopped server cannot serve an account, even if Android was offline.
		if !device.running() {
			if err := m.state.ReleaseEmulator(ctx, serial); err != nil {
				failures = append(failures, err)
			}
		}
	}
	if err := errors.Join(failures...); err != nil {
		return Status{}, err
	}
	if len(m.devices) == 0 {
		return m.base.Action(ctx, ActionStop)
	}
	return m.Status(ctx)
}

// OpenPlayer acquires the exclusive account reservation before restarting just
// the selected game. A failed launch restores the previous reservation.
func (m *MultiRunner) OpenPlayer(ctx context.Context, playerID string) error {
	if requested := player.Emulator(ctx); requested != "" && requested != m.selected {
		return fmt.Errorf("the selected emulator changed; try again")
	}
	s, err := m.Status(ctx)
	if err != nil {
		return err
	}
	if s.Mode != "local" || !s.Server.Running || !s.Android.Connected {
		return fmt.Errorf("enable local mode on the selected emulator before launching an account")
	}
	previous, err := m.state.EmulatorProfile(ctx, m.selected)
	if err != nil {
		return err
	}
	if err := m.state.SelectEmulatorPlayer(ctx, m.selected, playerID, true); err != nil {
		return err
	}
	device := m.devices[m.selected]
	err = device.launch(ctx)
	if err != nil {
		if previous.PlayerID != "" {
			err = errors.Join(err, m.state.SelectEmulatorPlayer(context.WithoutCancel(ctx), m.selected, previous.PlayerID, previous.Active))
		} else {
			err = errors.Join(err, m.state.ReleaseEmulator(context.WithoutCancel(ctx), m.selected))
		}
	}
	return err
}

// StartPanel preserves the root launcher's location and lifetime.
func (m *MultiRunner) StartPanel(ctx context.Context, open bool) error {
	return m.base.StartPanel(ctx, open)
}

func (r *NativeRunner) launch(ctx context.Context) error {
	return android.NewWithRunner(r.cfg.Android.Serial, r.cfg.Android.Package, r.cfg.Android.Activity, r.commands).Open(ctx)
}

func (r *NativeRunner) running() bool { return r.processPID("server", r.server) != 0 }
