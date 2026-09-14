package testprofile

import "github.com/Layen-lang/PTCGP-Private-Server/internal/protocol"

// Baseline is the fixed client contract used by offline handler tests.
func Baseline() protocol.Profile {
	return protocol.Profile{
		AppVersion:              "1.7.2",
		SDKVersion:              "v8.0.8",
		ResponseProtocolVersion: "v8.2.9",
		ClientSDKVersion:        "2.0.0",
		BaaSSDKVersion:          "Unity-3.13.1-c55a4366e",
		BuildHash:               "d221b73b872fb5c797af9f86b50f9520de259348",
		AssetBaseURL:            "https://prod-game-assets-app-41283.akamaized.net/",
		MasterMemoryAladdinHash: "d94a080180eb69af",
		AndroidAssetAladdinHash: "60e47d4533e095d6",
		PlayerAPIHost:           "player-api-prod.app-41283.com", BaaSHost: "1c04691f14f85ad285ebb3d2ffa4aef0.baas.nintendo.com",
	}
}
