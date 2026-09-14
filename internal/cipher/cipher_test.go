package cipher

import (
	"bytes"
	"encoding/hex"
	"testing"
)

var packerKey = []byte{
	0x31, 0x38, 0x24, 0xfb, 0xe9, 0x64, 0x5a, 0xa4,
	0xc3, 0x24, 0x72, 0x7f, 0x94, 0x26, 0xc5, 0xa5,
	0xe0, 0x51, 0xbf, 0x6a, 0xe5, 0x17, 0x53, 0x34,
	0xd6, 0xb8, 0x13, 0x29, 0x23, 0x1c, 0xe6, 0x1b,
}

var nonce01to11 = []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}

// Golden values generated from the Python reference implementation.

func TestCipherZeroKeyZeroNonceRoundGap(t *testing.T) {
	c := New(make([]byte, 32), make([]byte, 12))
	if c.roundGap != 2 {
		t.Errorf("round_gap = %d, want 2", c.roundGap)
	}
}

func TestCipherZeroKeyZeroNonce64Zeros(t *testing.T) {
	// Python: Cipher(bytes(32), bytes(12)).transform(bytes(64))
	want, _ := hex.DecodeString("281bf16b59503bf677eacc4b5b1409482a685a96109162eb9087564da1caf9e689557bb81656a67ff26f482bb2972814a12089dffa852112c2e6279c519af266")
	c := New(make([]byte, 32), make([]byte, 12))
	got := c.Transform(make([]byte, 64), 0)
	if !bytes.Equal(got, want) {
		t.Errorf("transform mismatch\ngot:  %x\nwant: %x", got, want)
	}
}

func TestCipherPackerKeyNonce16Zeros(t *testing.T) {
	// Python: Cipher(packerKey, bytes(range(12))).transform(bytes(16))
	want, _ := hex.DecodeString("facd69ba36d24530151dfecb37deac72")
	c := New(packerKey, nonce01to11)
	if c.roundGap != 2 {
		t.Errorf("round_gap = %d, want 2", c.roundGap)
	}
	got := c.Transform(make([]byte, 16), 0)
	if !bytes.Equal(got, want) {
		t.Errorf("transform mismatch\ngot:  %x\nwant: %x", got, want)
	}
}

func TestCipherSelfInverse(t *testing.T) {
	plain := []byte("hello world test data for cipher")
	c := New(packerKey, nonce01to11)
	encrypted := c.Transform(plain, 0)
	c2 := New(packerKey, nonce01to11)
	decrypted := c2.Transform(encrypted, 0)
	if !bytes.Equal(decrypted, plain) {
		t.Errorf("self-inverse failed: got %x, want %x", decrypted, plain)
	}
}

func TestCipherInitialState(t *testing.T) {
	c := New(packerKey, nonce01to11)
	// Python golden: full initial state for packerKey + nonce=[0..11]
	want := [16]uint32{
		0x40392b73, 0x4131615e, 0x215a7c6d, 0x32683831, // sigma
		0xfb243831, 0xa45a64e9, 0x7f7224c3, 0xa5c52694, // key[0..3]
		0x6abf51e0, 0x345317e5, 0x2913b8d6, 0x1be61c23, // key[4..7]
		0x00000000,                         // counter (0 in init)
		0x03020100, 0x07060504, 0x0b0a0908, // nonce
	}
	if c.state != want {
		t.Errorf("initial state mismatch\ngot:  %v\nwant: %v", c.state, want)
	}
}

func TestCipherMultiBlock(t *testing.T) {
	// Encrypting 65 bytes should process a 64-byte block + 1-byte block.
	// Self-inverse should still hold.
	plain := bytes.Repeat([]byte{0xAB}, 65)
	c1 := New(packerKey, nonce01to11)
	enc := c1.Transform(plain, 0)
	c2 := New(packerKey, nonce01to11)
	dec := c2.Transform(enc, 0)
	if !bytes.Equal(dec, plain) {
		t.Error("multi-block self-inverse failed")
	}
}
