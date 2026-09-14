package playerapi

import (
	"fmt"
	"sort"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"
)

const (
	playerAPIServicePrefix = "takasho.schema.lettuce_server.player_api."
)

// compatibilityRegistry turns the generated descriptors into a complete
// transport surface. Stateful handlers remain authoritative; this registry is
// used only when an approved Player API method has no local implementation.
type compatibilityRegistry struct {
	methods map[string]protoreflect.MethodDescriptor
}

func newCompatibilityRegistry() (*compatibilityRegistry, error) {
	registry := &compatibilityRegistry{methods: make(map[string]protoreflect.MethodDescriptor)}
	var descriptorErr error
	protoregistry.GlobalFiles.RangeFiles(func(file protoreflect.FileDescriptor) bool {
		services := file.Services()
		for serviceIndex := 0; serviceIndex < services.Len(); serviceIndex++ {
			service := services.Get(serviceIndex)
			if !strings.HasPrefix(string(service.FullName()), playerAPIServicePrefix) {
				continue
			}
			methods := service.Methods()
			for methodIndex := 0; methodIndex < methods.Len(); methodIndex++ {
				method := methods.Get(methodIndex)
				if method.IsStreamingClient() || method.IsStreamingServer() {
					descriptorErr = fmt.Errorf("Player API method %s is unexpectedly streaming", method.FullName())
					return false
				}
				fullMethod := "/" + string(service.FullName()) + "/" + string(method.Name())
				if _, exists := registry.methods[fullMethod]; exists {
					descriptorErr = fmt.Errorf("duplicate Player API method %s", fullMethod)
					return false
				}
				registry.methods[fullMethod] = method
			}
		}
		return descriptorErr == nil
	})
	if descriptorErr != nil {
		return nil, descriptorErr
	}
	if len(registry.methods) == 0 {
		return nil, fmt.Errorf("Player API registry is empty")
	}
	return registry, nil
}

func (r *compatibilityRegistry) request(fullMethod string) (proto.Message, bool) {
	method, ok := r.methods[fullMethod]
	if !ok {
		return nil, false
	}
	return dynamicpb.NewMessage(method.Input()), true
}

func (r *compatibilityRegistry) response(fullMethod string) (proto.Message, bool) {
	method, ok := r.methods[fullMethod]
	if !ok {
		return nil, false
	}
	return dynamicpb.NewMessage(method.Output()), true
}

func (r *compatibilityRegistry) paths() []string {
	paths := make([]string, 0, len(r.methods))
	for path := range r.methods {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func (r *compatibilityRegistry) serveUnknown(stream grpc.ServerStream) error {
	fullMethod, ok := grpc.MethodFromServerStream(stream)
	if !ok {
		return status.Error(codes.Unimplemented, "unknown gRPC method")
	}
	request, ok := r.request(fullMethod)
	if !ok {
		return status.Error(codes.Unimplemented, "method is outside the local Player API surface")
	}
	if err := stream.RecvMsg(request); err != nil {
		return status.Error(codes.InvalidArgument, "invalid request body")
	}
	response, _ := r.response(fullMethod)
	return stream.SendMsg(response)
}

func compatibilityMutation(fullMethod string) bool {
	separator := strings.LastIndexByte(fullMethod, '/')
	if separator < 0 || separator == len(fullMethod)-1 {
		return true
	}
	method := fullMethod[separator+1:]
	for _, prefix := range []string{"Echo", "Get", "Insight", "Is", "List", "May", "Sync"} {
		if strings.HasPrefix(method, prefix) {
			return false
		}
	}
	return true
}
