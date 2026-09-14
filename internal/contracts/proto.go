// Package contracts verifies generated protocol bindings against an approved profile.
package contracts

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	_ "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_debug_server/debug"
	_ "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// Expectations is the approved descriptor contract, independent of the observed build.
type Expectations struct {
	Services         int    `json:"services"`
	Methods          int    `json:"methods"`
	DescriptorSHA256 string `json:"descriptorSHA256"`
	Fingerprint      string `json:"fingerprint"`
}

const DeclarationOrderV1 = "declaration-order-v1"

// ProtoSummary is a stable description of generated Player API contracts.
type ProtoSummary struct {
	Files            int
	Services         int
	Methods          int
	DescriptorSHA256 string
}

// InspectProto counts and fingerprints generated Player API descriptors.
func InspectProto(algorithm string) (ProtoSummary, error) {
	if algorithm != "" && algorithm != DeclarationOrderV1 {
		return ProtoSummary{}, fmt.Errorf("unsupported descriptor fingerprint %q", algorithm)
	}
	var files []protoreflect.FileDescriptor
	protoregistry.GlobalFiles.RangeFiles(func(file protoreflect.FileDescriptor) bool {
		if isRPCContract(file.Path()) {
			files = append(files, file)
		}
		return true
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path() < files[j].Path() })
	hash := sha256.New()
	summary := ProtoSummary{Files: len(files)}
	for _, file := range files {
		services := file.Services()
		summary.Services += services.Len()
		for i := 0; i < services.Len(); i++ {
			summary.Methods += services.Get(i).Methods().Len()
		}
		descriptor := protodescriptor(file)
		if algorithm == DeclarationOrderV1 {
			canonicalizeDeclarations(descriptor)
		}
		data, err := proto.MarshalOptions{Deterministic: true}.Marshal(descriptor)
		if err != nil {
			return ProtoSummary{}, fmt.Errorf("marshal descriptor %s: %w", file.Path(), err)
		}
		_, _ = hash.Write([]byte(file.Path()))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(data)
	}
	summary.DescriptorSHA256 = hex.EncodeToString(hash.Sum(nil))
	return summary, nil
}

func isRPCContract(path string) bool {
	return strings.HasPrefix(path, "takasho/schema/lettuce_server/player_api/") ||
		strings.HasPrefix(path, "takasho/schema/lettuce_debug_server/debug/")
}

// VerifyProto enforces the expected service and method cardinality.
func VerifyProto(expected Expectations) (ProtoSummary, error) {
	if expected.Services <= 0 || expected.Methods <= 0 || len(expected.DescriptorSHA256) != 64 {
		return ProtoSummary{}, fmt.Errorf("missing or invalid approved proto expectations")
	}
	summary, err := InspectProto(expected.Fingerprint)
	if err != nil {
		return ProtoSummary{}, err
	}
	if summary.Services != expected.Services || summary.Methods != expected.Methods {
		return summary, fmt.Errorf("unexpected Player API contracts: got %d services/%d methods, want %d/%d", summary.Services, summary.Methods, expected.Services, expected.Methods)
	}
	if summary.DescriptorSHA256 != expected.DescriptorSHA256 {
		return summary, fmt.Errorf("unexpected descriptor SHA-256 %s", summary.DescriptorSHA256)
	}
	return summary, nil
}

func protodescriptor(file protoreflect.FileDescriptor) *descriptorpb.FileDescriptorProto {
	return protodescriptorToProto(file)
}
