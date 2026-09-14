package transport

import (
	"testing"

	playerapi "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
)

func TestCodecRoundTrip(t *testing.T) {
	t.Parallel()
	want := &playerapi.SystemAuthorizeV1_Types_Request{DeviceAccount: "fixture-device"}
	codec := NewCodec()
	data, err := codec.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	got := new(playerapi.SystemAuthorizeV1_Types_Request)
	if err := codec.Unmarshal(data, got); err != nil {
		t.Fatal(err)
	}
	if got.GetDeviceAccount() != want.GetDeviceAccount() {
		t.Fatalf("device account = %q, want %q", got.GetDeviceAccount(), want.GetDeviceAccount())
	}
}
