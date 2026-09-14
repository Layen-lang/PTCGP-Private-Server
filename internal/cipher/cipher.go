// Package cipher implements the custom symmetric block cipher used by the
// Takasho player API for encrypting and decrypting gRPC payloads.
//
// This is NOT a standard cipher. It uses a custom sigma constant and a
// variable round count selected at initialization from the key/nonce state.
package cipher

import (
	"encoding/binary"
	"math/bits"
)

// sigma is the non-standard constant used in place of ChaCha20's "expand 32-byte k".
var sigma = [16]byte{'s', '+', '9', '@', '^', 'a', '1', 'A', 'm', '|', 'Z', '!', '1', '8', 'h', '2'}

// roundGaps table indexed by a 4-bit value derived from the initial state.
var roundGaps = [16]int{2, 3, 2, 4, 3, 4, 4, 1, 1, 1, 2, 3, 3, 2, 1, 4}

// Cipher is the symmetric block cipher. It is safe to call Transform multiple
// times on the same Cipher instance (each call is independent given counter=0).
type Cipher struct {
	state    [16]uint32
	roundGap int
}

// New initializes a Cipher with the given 32-byte key and 12-byte nonce.
func New(key, nonce []byte) *Cipher {
	var state [16]uint32

	// state[0..3] = sigma words (little-endian)
	for i := 0; i < 4; i++ {
		state[i] = binary.LittleEndian.Uint32(sigma[i*4 : i*4+4])
	}
	// state[4..11] = key words (little-endian)
	for i := 0; i < 8; i++ {
		state[i+4] = binary.LittleEndian.Uint32(key[i*4 : i*4+4])
	}
	// state[12] = counter (initialized to 0; overwritten per-block in Transform)
	state[12] = 0
	// state[13..15] = nonce words (little-endian)
	for i := 0; i < 3; i++ {
		state[i+13] = binary.LittleEndian.Uint32(nonce[i*4 : i*4+4])
	}

	// Compute round_gap from the initial state.
	u := ((state[9] + state[4]) ^ state[11]) + ((state[14] + state[13]) ^ state[15])
	idx := ((u >> 7) & 2) | ((u >> 2) & 1) | ((u >> 13) & 4) | ((u >> 2) & 8)
	rg := roundGaps[idx]

	return &Cipher{state: state, roundGap: rg}
}

// Transform encrypts or decrypts body starting at the given counter value.
// The cipher is symmetric: Transform(Transform(body)) == body.
func (c *Cipher) Transform(body []byte, counter uint32) []byte {
	result := make([]byte, len(body))
	length := len(body)
	offset := 0

	for length > 0 {
		blockLen := length
		if blockLen > 64 {
			blockLen = 64
		}

		// Copy initial state and set counter for this block.
		var st [16]uint32
		st = c.state
		st[12] = counter

		rounds := 10 - c.roundGap
		if c.roundGap < 10 {
			if rounds < 1 {
				rounds = 1
			}
			for i := 0; i < rounds; i++ {
				// Column round 0: (0, 4, 8, 12)
				st[0] += st[4]
				st[12] = bits.RotateLeft32(st[12]^st[0], -16)
				st[8] += st[12]
				st[4] = bits.RotateLeft32(st[4]^st[8], -20)
				st[0] += st[4]
				st[12] = bits.RotateLeft32(st[12]^st[0], -24)
				st[8] += st[12]
				st[4] = bits.RotateLeft32(st[4]^st[8], -25)

				// Column round 1: (1, 5, 9, 13)
				st[1] += st[5]
				st[13] = bits.RotateLeft32(st[13]^st[1], -16)
				st[9] += st[13]
				st[5] = bits.RotateLeft32(st[5]^st[9], -20)
				st[1] += st[5]
				st[13] = bits.RotateLeft32(st[13]^st[1], -24)
				st[9] += st[13]
				st[5] = bits.RotateLeft32(st[5]^st[9], -25)

				// Column round 2: (2, 6, 10, 14)
				st[2] += st[6]
				st[14] = bits.RotateLeft32(st[14]^st[2], -16)
				st[10] += st[14]
				st[6] = bits.RotateLeft32(st[6]^st[10], -20)
				st[2] += st[6]
				st[14] = bits.RotateLeft32(st[14]^st[2], -24)
				st[10] += st[14]
				st[6] = bits.RotateLeft32(st[6]^st[10], -25)

				// Column round 3: (3, 7, 11, 15)
				st[3] += st[7]
				st[15] = bits.RotateLeft32(st[15]^st[3], -16)
				st[11] += st[15]
				st[7] = bits.RotateLeft32(st[7]^st[11], -20)
				st[3] += st[7]
				st[15] = bits.RotateLeft32(st[15]^st[3], -24)
				st[11] += st[15]
				st[7] = bits.RotateLeft32(st[7]^st[11], -25)

				// Diagonal round 0: (0, 5, 10, 15)
				st[0] += st[5]
				st[15] = bits.RotateLeft32(st[15]^st[0], -16)
				st[10] += st[15]
				st[5] = bits.RotateLeft32(st[5]^st[10], -20)
				st[0] += st[5]
				st[15] = bits.RotateLeft32(st[15]^st[0], -24)
				st[10] += st[15]
				st[5] = bits.RotateLeft32(st[5]^st[10], -25)

				// Diagonal round 1: (1, 6, 11, 12)
				st[1] += st[6]
				st[12] = bits.RotateLeft32(st[12]^st[1], -16)
				st[11] += st[12]
				st[6] = bits.RotateLeft32(st[6]^st[11], -20)
				st[1] += st[6]
				st[12] = bits.RotateLeft32(st[12]^st[1], -24)
				st[11] += st[12]
				st[6] = bits.RotateLeft32(st[6]^st[11], -25)

				// Diagonal round 2: (2, 7, 8, 13)
				st[2] += st[7]
				st[13] = bits.RotateLeft32(st[13]^st[2], -16)
				st[8] += st[13]
				st[7] = bits.RotateLeft32(st[7]^st[8], -20)
				st[2] += st[7]
				st[13] = bits.RotateLeft32(st[13]^st[2], -24)
				st[8] += st[13]
				st[7] = bits.RotateLeft32(st[7]^st[8], -25)

				// Diagonal round 3: (3, 4, 9, 14)
				st[3] += st[4]
				st[14] = bits.RotateLeft32(st[14]^st[3], -16)
				st[9] += st[14]
				st[4] = bits.RotateLeft32(st[4]^st[9], -20)
				st[3] += st[4]
				st[14] = bits.RotateLeft32(st[14]^st[3], -24)
				st[9] += st[14]
				st[4] = bits.RotateLeft32(st[4]^st[9], -25)
			}
		}

		// Add initial state back.
		for i := 0; i < 16; i++ {
			st[i] += c.state[i]
		}
		// Add counter back to word 12.
		st[12] += counter

		// XOR keystream with body bytes.
		for i := 0; i < blockLen; i++ {
			word := st[i/4]
			keyByte := byte(word >> (uint(i%4) * 8))
			result[offset+i] = body[offset+i] ^ keyByte
		}

		length -= blockLen
		offset += blockLen
		counter++
	}

	return result
}
