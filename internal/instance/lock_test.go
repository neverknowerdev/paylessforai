package instance

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestAcquirePreventsConcurrentInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "paylessforai.lock")
	first, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Acquire(path)
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("expected already-running error, got lock=%v err=%v", second, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	third.Close()
}
