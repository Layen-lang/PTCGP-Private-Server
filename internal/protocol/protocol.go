// Package protocol contains the client contract accepted by the local server.
package protocol

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc/metadata"
)

// Profile contains the client values belonging to one validated release.
type Profile struct {
	AppVersion              string `json:"appVersion"`
	SDKVersion              string `json:"sdkVersion"`
	ResponseProtocolVersion string `json:"responseProtocolVersion"`
	ClientSDKVersion        string `json:"clientSDKVersion"`
	BaaSSDKVersion          string `json:"baasSDKVersion"`
	BuildHash               string `json:"buildHash"`
	AssetBaseURL            string `json:"assetBaseURL"`
	MasterMemoryAladdinHash string `json:"masterMemoryAladdinHash"`
	AndroidAssetAladdinHash string `json:"androidAssetAladdinHash"`
	PlayerAPIHost           string `json:"playerAPIHost"`
	BaaSHost                string `json:"baasHost"`
}

const (
	// Response metadata keys used by the Takasho client bootstrap.
	MasterMemoryAladdinHeader = "x-takasho-response-master-memory-aladdin-hash"
	AndroidAssetAladdinHeader = "x-takasho-response-asset-aladdin-hash-android"
	RequestedTimestampHeader  = "x-takasho-requested-timestamp"
	ProtocolVersionHeader     = "x-takasho-protocol-version"
)

// MetadataPolicy controls how strictly incoming gRPC metadata is checked.
type MetadataPolicy struct {
	Strict  bool
	Profile Profile
}

// Validate checks stable version metadata while tolerating spelling variants
// observed across client builds. Authorize is allowed without a session token.
func (p MetadataPolicy) Validate(ctx context.Context, fullMethod string) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		if p.Strict {
			return fmt.Errorf("missing gRPC metadata")
		}
		return nil
	}
	checks := []struct {
		names []string
		want  string
	}{
		{[]string{"x-takasho-app-version"}, p.Profile.AppVersion},
		{[]string{"x-takasho-protocol-version"}, p.Profile.SDKVersion},
		{[]string{"x-takasho-build-version"}, p.Profile.BuildHash},
		{[]string{"x-takasho-sdk-version"}, p.Profile.ClientSDKVersion},
	}
	for _, check := range checks {
		got, present := first(md, check.names...)
		if present && got != check.want {
			return fmt.Errorf("unsupported metadata %s=%q", check.names[0], got)
		}
		if p.Strict && !present {
			return fmt.Errorf("missing metadata %s", check.names[0])
		}
	}
	if !strings.HasSuffix(fullMethod, "/AuthorizeV1") {
		if _, present := first(md, "x-takasho-session-token"); p.Strict && !present {
			return fmt.Errorf("missing session metadata")
		}
	}
	return nil
}

func first(md metadata.MD, names ...string) (string, bool) {
	for _, name := range names {
		if values := md.Get(name); len(values) != 0 && values[0] != "" {
			return values[0], true
		}
	}
	return "", false
}
