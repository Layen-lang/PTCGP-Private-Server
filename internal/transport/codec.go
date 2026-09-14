// Package transport adapts the Takasho packed protobuf wire format to gRPC.
package transport

import (
	"fmt"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/packer"
	"google.golang.org/protobuf/proto"
)

// Codec marshals protobuf messages through the game's authenticated packer.
type Codec struct {
	packer *packer.Packer
}

// NewCodec returns a codec compatible with the official client's gRPC bodies.
func NewCodec() *Codec { return &Codec{packer: packer.DefaultPacker()} }

// Name is the content subtype advertised to grpc-go.
func (*Codec) Name() string { return "proto" }

// Marshal serializes and packs a protobuf message.
func (c *Codec) Marshal(value any) ([]byte, error) {
	message, ok := value.(proto.Message)
	if !ok {
		return nil, fmt.Errorf("takasho codec: %T is not a protobuf message", value)
	}
	raw, err := proto.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("takasho codec: marshal: %w", err)
	}
	packed, err := c.packer.Pack(raw)
	if err != nil {
		return nil, fmt.Errorf("takasho codec: pack: %w", err)
	}
	return packed, nil
}

// Unmarshal unpacks and deserializes a protobuf message.
func (c *Codec) Unmarshal(data []byte, value any) error {
	message, ok := value.(proto.Message)
	if !ok {
		return fmt.Errorf("takasho codec: %T is not a protobuf message", value)
	}
	raw, err := c.packer.Unpack(data)
	if err != nil {
		return fmt.Errorf("takasho codec: unpack: %w", err)
	}
	if err := proto.Unmarshal(raw, message); err != nil {
		return fmt.Errorf("takasho codec: unmarshal: %w", err)
	}
	return nil
}
