// Package packer implements the Takasho API pack/unpack protocol:
// deflate (raw zlib) + HMAC-SHA256 authentication + custom cipher encryption.
//
// All gRPC payloads sent to and received from the game server are wrapped
// with this packer. The key is hardcoded by the game client.
package packer

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/cipher"
)

// ErrInvalidMAC is returned when the HMAC of a received payload does not match.
var ErrInvalidMAC = errors.New("invalid MAC")

// packerKey is the hardcoded 32-byte symmetric key extracted from the game client.
var packerKey = []byte{
	0x31, 0x38, 0x24, 0xfb, 0xe9, 0x64, 0x5a, 0xa4,
	0xc3, 0x24, 0x72, 0x7f, 0x94, 0x26, 0xc5, 0xa5,
	0xe0, 0x51, 0xbf, 0x6a, 0xe5, 0x17, 0x53, 0x34,
	0xd6, 0xb8, 0x13, 0x29, 0x23, 0x1c, 0xe6, 0x1b,
}

// Packer handles deflating, authenticating, and encrypting outgoing payloads,
// and the reverse for incoming payloads.
type Packer struct {
	key []byte
}

// DefaultPacker returns a Packer using the hardcoded game key.
func DefaultPacker() *Packer {
	key := make([]byte, len(packerKey))
	copy(key, packerKey)
	return &Packer{key: key}
}

// Pack deflates and encrypts body using a randomly generated nonce.
func (p *Packer) Pack(body []byte) ([]byte, error) {
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("packer: generate nonce: %w", err)
	}
	return p.PackWithNonce(nonce, body)
}

// PackWithNonce deflates and encrypts body using the supplied nonce.
// nonce must be 12 bytes.
func (p *Packer) PackWithNonce(nonce, body []byte) ([]byte, error) {
	mac := p.ComputeHash(nonce, body)
	deflated, err := p.deflate(mac, body)
	if err != nil {
		return nil, fmt.Errorf("packer: deflate: %w", err)
	}
	c := cipher.New(p.key, nonce)
	encrypted := c.Transform(deflated, 0)

	result := make([]byte, 0, 12+len(encrypted))
	result = append(result, nonce...)
	result = append(result, encrypted...)
	return result, nil
}

// Unpack decrypts and inflates the received payload, verifying its HMAC.
func (p *Packer) Unpack(encrypted []byte) ([]byte, error) {
	if len(encrypted) < 12 {
		return nil, fmt.Errorf("packer: payload too short (%d bytes)", len(encrypted))
	}
	nonce := encrypted[:12]
	c := cipher.New(p.key, nonce)
	decrypted := c.Transform(encrypted[12:], 0)

	body, header, err := p.inflate(decrypted)
	if err != nil {
		return nil, fmt.Errorf("packer: inflate: %w", err)
	}

	expected := p.ComputeHash(nonce, body)
	if !hmac.Equal(expected, header) {
		return nil, ErrInvalidMAC
	}
	return body, nil
}

// ComputeHash computes HMAC-SHA256(key||nonce, body).
func (p *Packer) ComputeHash(nonce, body []byte) []byte {
	combined := make([]byte, len(p.key)+len(nonce))
	copy(combined, p.key)
	copy(combined[len(p.key):], nonce)
	h := hmac.New(sha256.New, combined)
	h.Write(body)
	return h.Sum(nil)
}

// deflate compresses (contentHeader + body) using raw deflate (zlib compressed,
// then 2-byte header and 4-byte checksum stripped).
// Mirrors Python: zlib.compress(header + body)[2:-4]
func (p *Packer) deflate(contentHeader, body []byte) ([]byte, error) {
	content := make([]byte, 0, len(contentHeader)+len(body))
	content = append(content, contentHeader...)
	content = append(content, body...)

	var buf bytes.Buffer
	w, err := zlib.NewWriterLevel(&buf, zlib.DefaultCompression)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(content); err != nil {
		_ = w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	zlibBytes := buf.Bytes()
	// Strip 2-byte zlib header and 4-byte Adler32 checksum.
	if len(zlibBytes) < 6 {
		return nil, fmt.Errorf("deflated output too short: %d bytes", len(zlibBytes))
	}
	raw := make([]byte, len(zlibBytes)-6)
	copy(raw, zlibBytes[2:len(zlibBytes)-4])
	return raw, nil
}

// inflate decompresses raw deflate bytes (no zlib header/trailer).
// Mirrors Python: zlib.decompressobj(-zlib.MAX_WBITS).decompress(body)
// Returns (payload_without_header, header) where header is the first 0x20 bytes.
func (p *Packer) inflate(body []byte) (payload, header []byte, err error) {
	r := flate.NewReader(bytes.NewReader(body))
	defer r.Close()

	content, err := io.ReadAll(r)
	if err != nil {
		return nil, nil, err
	}
	if len(content) < 0x20 {
		return nil, nil, fmt.Errorf("inflated content too short: %d bytes", len(content))
	}
	hdr := make([]byte, 0x20)
	copy(hdr, content[:0x20])
	pl := make([]byte, len(content)-0x20)
	copy(pl, content[0x20:])
	return pl, hdr, nil
}
