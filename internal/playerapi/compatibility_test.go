package playerapi

import (
	"context"
	"net"
	"testing"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func TestCompatibilityRegistryCoversEveryPlayerAPIMethod(t *testing.T) {
	t.Parallel()
	registry, err := newCompatibilityRegistry()
	if err != nil {
		t.Fatal(err)
	}
	paths := registry.paths()
	if len(paths) == 0 {
		t.Fatal("Player API registry is empty")
	}
	for _, path := range paths {
		request, requestOK := registry.request(path)
		response, responseOK := registry.response(path)
		if !requestOK || request == nil || !responseOK || response == nil {
			t.Errorf("Player API method %s has incomplete dynamic messages", path)
		}
	}
}

func TestCompatibilityRegistryServesEveryPlayerAPIContract(t *testing.T) {
	t.Parallel()
	registry, err := newCompatibilityRegistry()
	if err != nil {
		t.Fatal(err)
	}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer(
		grpc.ForceServerCodec(transport.NewCodec()),
		grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
			return registry.serveUnknown(stream)
		}),
	)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	connection, err := grpc.NewClient(
		"passthrough:///compatibility",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(transport.NewCodec())),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	for _, path := range registry.paths() {
		request, _ := registry.request(path)
		response, _ := registry.response(path)
		if err := connection.Invoke(context.Background(), path, request, response); err != nil {
			t.Errorf("Player API method %s: %v", path, err)
		}
	}
}
