package control

import (
	"context"
	"testing"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/configuration"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/provision"
)

func TestClosingPanelCancelsPreparationAndPreventsRestart(t *testing.T) {
	journal, err := NewJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{journal: journal}
	started := make(chan struct{})
	h.SetPreparation(provision.New(configuration.Config{}, t.TempDir(), t.TempDir()), func(ctx context.Context, _ string) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	if !h.StartPreparation("device") {
		t.Fatal("preparation did not start")
	}
	<-started
	closed := make(chan struct{})
	go func() { h.ClosePreparation(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("preparation did not stop")
	}
	if h.currentOperation() != "" || h.StartPreparation("device") {
		t.Fatal("closed panel accepted more work")
	}
}
