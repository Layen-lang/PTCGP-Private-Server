package control

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/configuration"
)

type caStoreRunner struct {
	apex  bool
	calls []string
}

func (r *caStoreRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	call := strings.Join(args, " ")
	r.calls = append(r.calls, call)
	if strings.Contains(call, "pidof") && strings.Contains(call, "zygote64") {
		return []byte("101\n102\n"), nil
	}
	if strings.Contains(call, "test -d") && strings.Contains(call, conscryptAndroidCAStore.target) && !r.apex {
		return nil, fmt.Errorf("APEX store absent")
	}
	return nil, nil
}

func TestAndroidCAStoresIncludeConscryptWhenAvailable(t *testing.T) {
	commands := &caStoreRunner{apex: true}
	r := &NativeRunner{commands: commands}
	stores := r.androidCAStores(context.Background(), "serial")
	if len(stores) != 2 || stores[0] != legacyAndroidCAStore || stores[1] != conscryptAndroidCAStore {
		t.Fatalf("stores=%+v", stores)
	}

	commands.apex = false
	stores = r.androidCAStores(context.Background(), "serial")
	if len(stores) != 1 || stores[0] != legacyAndroidCAStore {
		t.Fatalf("legacy stores=%+v", stores)
	}
}

func TestInstallConscryptCAUsesGuardedBindMount(t *testing.T) {
	commands := &caStoreRunner{apex: true}
	r := &NativeRunner{
		commands: commands,
		cfg: configuration.Config{Runtime: configuration.Runtime{
			CertificateAuthority: "ca.pem",
		}},
	}
	if err := r.installAndroidCAStore(context.Background(), "serial", "12345678.0", conscryptAndroidCAStore); err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(commands.calls, "\n")
	for _, want := range []string{
		"rm -rf",
		conscryptAndroidCAStore.staging,
		"push ca.pem /data/local/tmp/ptcgp-push-12345678.0",
		"chcon u:object_r:system_security_cacerts_file:s0",
		"mount --bind",
		"nsenter -t 101 -m -- mount --bind",
		"nsenter -t 102 -m -- test -f",
		conscryptAndroidCAStore.target,
		"test -f",
	} {
		if !strings.Contains(calls, want) {
			t.Fatalf("missing %q in calls:\n%s", want, calls)
		}
	}
}

func TestUnmountConscryptCAOnlyTargetsOurOverlay(t *testing.T) {
	commands := &caStoreRunner{}
	r := &NativeRunner{commands: commands}
	if err := r.unmountAndroidCAStores(context.Background(), "serial"); err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(commands.calls, "\n")
	if !strings.Contains(calls, "ptcgp-conscrypt-cacerts /apex/com.android.conscrypt/cacerts") ||
		!strings.Contains(calls, "/proc/self/mountinfo") ||
		!strings.Contains(calls, "nsenter -t 101 -m -- sh -c") {
		t.Fatalf("Conscrypt unmount is not scoped to the managed overlay:\n%s", calls)
	}
	if strings.Contains(calls, "/proc/mounts") {
		t.Fatalf("base APEX could be mistaken for the managed overlay:\n%s", calls)
	}
}

func TestCAStatusChecksAndroidAppNamespaces(t *testing.T) {
	commands := &caStoreRunner{apex: true}
	r := &NativeRunner{commands: commands}
	if !r.androidCAInstalled(context.Background(), "serial", "12345678.0") {
		t.Fatal("certificate visible in root and zygote namespaces was not detected")
	}
	calls := strings.Join(commands.calls, "\n")
	if !strings.Contains(calls, "nsenter -t 101 -m -- test -f") ||
		!strings.Contains(calls, conscryptAndroidCAStore.target+"/12345678.0") {
		t.Fatalf("status did not inspect app trust stores:\n%s", calls)
	}
}
