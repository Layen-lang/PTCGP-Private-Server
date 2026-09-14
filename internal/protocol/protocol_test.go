package protocol_test

import (
	"context"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/protocol"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/testprofile"
	"google.golang.org/grpc/metadata"
	"testing"
)

func TestProfilesAreIndependent(t *testing.T) {
	first := testprofile.Baseline()
	second := first
	second.AppVersion = "9.9.9"
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-takasho-app-version", first.AppVersion, "x-takasho-protocol-version", first.SDKVersion, "x-takasho-build-version", first.BuildHash, "x-takasho-sdk-version", first.ClientSDKVersion))
	a := protocol.MetadataPolicy{Strict: true, Profile: first}
	b := protocol.MetadataPolicy{Strict: true, Profile: second}
	if err := a.Validate(ctx, "/System/AuthorizeV1"); err != nil {
		t.Fatal(err)
	}
	if err := b.Validate(ctx, "/System/AuthorizeV1"); err == nil {
		t.Fatal("second profile accepted first version")
	}
	if err := a.Validate(ctx, "/System/AuthorizeV1"); err != nil {
		t.Fatal("profile state leaked", err)
	}
}
