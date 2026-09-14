package contracts

import (
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"sort"
)

func protodescriptorToProto(file protoreflect.FileDescriptor) *descriptorpb.FileDescriptorProto {
	return protodesc.ToFileDescriptorProto(file)
}

// Only sibling message declaration order is normalized. Field numbers, types,
// names, enum ordering/defaults, oneofs, services and all options remain checked.
func canonicalizeDeclarations(file *descriptorpb.FileDescriptorProto) {
	var sortMessages func([]*descriptorpb.DescriptorProto)
	sortMessages = func(messages []*descriptorpb.DescriptorProto) {
		sort.Slice(messages, func(i, j int) bool { return messages[i].GetName() < messages[j].GetName() })
		for _, message := range messages {
			sortMessages(message.NestedType)
		}
	}
	sortMessages(file.MessageType)
}
