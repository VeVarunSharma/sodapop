//go:build windows

package engine

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/VeVarunSharma/sodapop/internal/securefs"
)

func TestWindowsIndexLockHonorsCancellationAndRelease(t *testing.T) {
	home := t.TempDir()
	if err := securefs.MkdirAllPrivate(home); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	first, err := lockIndex(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := lockIndex(ctx, root); !errors.Is(err, context.DeadlineExceeded) {
		first.Close()
		t.Fatalf("contended lock = %v, want deadline exceeded", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := lockIndex(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsIndexLockWaitsForConcurrentProtection(t *testing.T) {
	home := t.TempDir()
	if err := securefs.MkdirAllPrivate(home); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	creator, err := root.OpenFile("sodapop-sessions.lock", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer creator.Close()
	protected := make(chan error, 1)
	go func() {
		time.Sleep(25 * time.Millisecond)
		protected <- securefs.ProtectFile(creator)
	}()
	lock, err := lockIndex(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-protected; err != nil {
		lock.Close()
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
}
