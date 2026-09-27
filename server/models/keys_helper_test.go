package models

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/meshery/meshkit/database"
	"github.com/meshery/meshkit/logger"
)

// seedKeysPanickingLogger panics on its first Error call, then delegates every
// later call to the wrapped logger. It stands in for any unexpected fault on
// SeedKeys' parse goroutine: where the panic comes from is irrelevant to the
// recover at the spawn site, which must contain it no matter which statement
// faulted.
type seedKeysPanickingLogger struct {
	logger.Handler
	mu    sync.Mutex
	calls int
}

func (l *seedKeysPanickingLogger) Error(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	if l.calls == 1 {
		panic("simulated fault on the parse goroutine")
	}
	l.Handler.Error(err)
}

// TestSeedKeysChildGoroutinePanicIsContained pins the residual path
// meshery/meshery#21586 left documented but open: RunSeedStage recovers only
// panics on the wrapped stage's own goroutine, while SeedKeys does its parsing
// on a spawned goroutine. A panic there used to unwind to the top of that
// goroutine and terminate the whole server at boot.
//
// The empty CSV makes Parse return before sending any row, so the only fault
// in play is the injected panic, and Parse's deferred cancel still closes its
// context while unwinding, letting SeedKeys' select loop observe Done and
// return. Without the recover at the spawn site this test does not fail
// cleanly - the unrecovered panic kills the test binary itself.
func TestSeedKeysChildGoroutinePanicIsContained(t *testing.T) {
	db, err := database.New(database.Options{Engine: database.SQLITE, Filename: ":memory:"})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	base, sink := capturingTestLogger(t)
	krh, err := NewKeysRegistrationHelper(&db, &seedKeysPanickingLogger{Handler: base})
	if err != nil {
		t.Fatalf("build keys helper: %v", err)
	}

	keysPath := filepath.Join(t.TempDir(), "keys.csv")
	if err := os.WriteFile(keysPath, []byte{}, 0o644); err != nil {
		t.Fatalf("write keys fixture: %v", err)
	}

	laterStageRan := false
	RunSeedStage(base, "user keys", func() {
		krh.SeedKeys(keysPath)
	})
	RunSeedStage(base, "models", func() {
		laterStageRan = true
	})

	if !laterStageRan {
		t.Fatal("a panic on the user-keys stage's child goroutine prevented a later stage from running")
	}
	// The recovered report races SeedKeys' return: Done closes during the
	// panic unwind, before the deferred recover logs, so the select loop can
	// return first. Poll rather than asserting immediately.
	deadline := time.Now().Add(10 * time.Second)
	for !sink.reports(t, ErrSeedingStagePanicCode) {
		if time.Now().After(deadline) {
			t.Fatalf("the child-goroutine fault was not reported as %s, so containing it swallowed it; emitted: %+v", ErrSeedingStagePanicCode, sink.records(t))
		}
		time.Sleep(10 * time.Millisecond)
	}
}
