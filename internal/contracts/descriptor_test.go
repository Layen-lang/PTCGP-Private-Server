package contracts

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"testing"
)

func TestCanonicalDeclarationOrderStillDetectsChangedFields(t *testing.T) {
	first := &descriptorpb.FileDescriptorProto{MessageType: []*descriptorpb.DescriptorProto{
		{Name: proto.String("Response")},
		{Name: proto.String("Request"), Field: []*descriptorpb.FieldDescriptorProto{{Name: proto.String("id"), Number: proto.Int32(1)}}},
	}}
	second := proto.Clone(first).(*descriptorpb.FileDescriptorProto)
	second.MessageType[0], second.MessageType[1] = second.MessageType[1], second.MessageType[0]
	canonicalizeDeclarations(first)
	canonicalizeDeclarations(second)
	if !proto.Equal(first, second) {
		t.Fatal("message ordering changed fingerprint")
	}
	second.MessageType[0].Field[0].Number = proto.Int32(2)
	canonicalizeDeclarations(second)
	if proto.Equal(first, second) {
		t.Fatal("field number change was hidden")
	}
}
