package android

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls [][]string
	local bool
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := append([]string{name}, args...)
	f.calls = append(f.calls, call)
	joined := strings.Join(args, " ")
	switch {
	case strings.HasSuffix(joined, "get-state"):
		return []byte("device\n"), nil
	case strings.Contains(joined, "cat /system/etc/hosts"):
		if f.local {
			return []byte("127.0.0.1 localhost\n" + localHostsMarker + "\n"), nil
		}
		return []byte("127.0.0.1 localhost\n"), nil
	case strings.Contains(joined, "am force-stop"), strings.Contains(joined, "am start -n"):
		return []byte("OK"), nil
	}
	return nil, fmt.Errorf("unexpected call")
}

func TestOpenUsesExpectedADBSequence(t *testing.T) {
	runner := &fakeRunner{local: true}
	controller := NewWithRunner("emulator-5554", "jp.pokemon.pokemontcgp", "com.unity3d.player.UnityPlayerActivity", runner)
	if err := controller.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(runner.calls))
	for i, call := range runner.calls {
		got[i] = strings.Join(call, " ")
	}
	want := []string{"adb -s emulator-5554 get-state", "adb -s emulator-5554 shell cat /system/etc/hosts", "adb -s emulator-5554 shell am force-stop jp.pokemon.pokemontcgp", "adb -s emulator-5554 shell am start -n jp.pokemon.pokemontcgp/com.unity3d.player.UnityPlayerActivity"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("calls=\n%v\nwant=\n%v", got, want)
	}
}

func TestOpenRefusesOfficialRouting(t *testing.T) {
	runner := &fakeRunner{}
	controller := NewWithRunner("emulator-5554", "package", "activity", runner)
	if err := controller.Open(context.Background()); err == nil || !strings.Contains(err.Error(), "not local") {
		t.Fatalf("error = %v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(runner.calls))
	}
}
