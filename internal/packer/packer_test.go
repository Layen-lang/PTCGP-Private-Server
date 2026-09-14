package packer

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// goldenPackedHex is Python's packer.pack_with_nonce(bytes(12), b'test payload for packing').
// Go's deflate may produce different compressed bytes than Python's zlib, but Go must be
// able to unpack Python's output (interoperability check).
const goldenPackedHex = "0000000000000000000000007b194429cbd980f3045b090ad7855e5cdbef87073b75418b00ce0a5849c828b37149c5a1885b92ecfc8063f66be99043cd88759e6927ccd1d21a"

var testBody = []byte("test payload for packing")

// TestPackerUnpackPythonGolden verifies that Go can unpack a payload produced by the Python packer.
func TestPackerUnpackPythonGolden(t *testing.T) {
	p := DefaultPacker()
	packed, _ := hex.DecodeString(goldenPackedHex)
	got, err := p.Unpack(packed)
	if err != nil {
		t.Fatalf("Unpack python golden: %v", err)
	}
	if !bytes.Equal(got, testBody) {
		t.Errorf("unpack mismatch\ngot:  %x\nwant: %x", got, testBody)
	}
}

func TestPackerRoundTripZeroNonce(t *testing.T) {
	p := DefaultPacker()
	packed, err := p.PackWithNonce(make([]byte, 12), testBody)
	if err != nil {
		t.Fatalf("PackWithNonce: %v", err)
	}
	got, err := p.Unpack(packed)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	if !bytes.Equal(got, testBody) {
		t.Errorf("round-trip mismatch: got %x, want %x", got, testBody)
	}
}

func TestPackerRoundTripRandomNonce(t *testing.T) {
	p := DefaultPacker()
	packed, err := p.Pack(testBody)
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	got, err := p.Unpack(packed)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	if !bytes.Equal(got, testBody) {
		t.Errorf("round-trip mismatch: got %x, want %x", got, testBody)
	}
}

func TestPackerRoundTripLargeBody(t *testing.T) {
	p := DefaultPacker()
	body := bytes.Repeat([]byte("hello world! "), 200)
	packed, err := p.Pack(body)
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	got, err := p.Unpack(packed)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("large body round-trip failed")
	}
}

func TestPackerInvalidMAC(t *testing.T) {
	p := DefaultPacker()
	packed, err := p.Pack(testBody)
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	// Corrupt one byte in the middle (after the 12-byte nonce).
	// Corruption may cause either an inflate error or a MAC mismatch —
	// both indicate tampered data. The important invariant is that Unpack
	// never returns nil error on corrupted input.
	packed[20] ^= 0xFF
	_, err = p.Unpack(packed)
	if err == nil {
		t.Fatal("expected error on corrupted data, got nil")
	}
}

func TestComputeHash(t *testing.T) {
	p := DefaultPacker()
	// Different inputs should produce different hashes.
	h1 := p.ComputeHash(make([]byte, 12), []byte("data1"))
	h2 := p.ComputeHash(make([]byte, 12), []byte("data2"))
	if bytes.Equal(h1, h2) {
		t.Error("different bodies should produce different hashes")
	}
	// Same inputs should be deterministic.
	h3 := p.ComputeHash(make([]byte, 12), []byte("data1"))
	if !bytes.Equal(h1, h3) {
		t.Error("hash is not deterministic")
	}
}
