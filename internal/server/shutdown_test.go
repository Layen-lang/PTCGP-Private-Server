package server

import (
	"net"
	"net/http"
	"testing"
	"time"
)

func TestShutdownClosesNeverEndingStreams(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	ended := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(started)
		// A traffic feed or game stream: it only ends with its connection.
		<-r.Context().Done()
		close(ended)
	})}
	go server.Serve(listener)
	response, err := http.Get("http://" + listener.Addr().String() + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	<-started

	begin := time.Now()
	if err := shutdownServers(200*time.Millisecond, server); err != nil {
		t.Fatalf("shutdown error: %v", err)
	}
	if elapsed := time.Since(begin); elapsed > 2*time.Second {
		t.Fatalf("shutdown waited %s for a stream that never ends", elapsed)
	}
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("the stream handler was not cancelled")
	}
}
