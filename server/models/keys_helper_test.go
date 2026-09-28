package models

import (
	"os"
	"path/filepath"
	"strings"
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

// TestSeedKeysMissingRegisterColumnIsLoud pins the silent-zero-seed defect: when
// the register header ("Local Provider") is renamed or dropped from the keys
// file, the row predicate used to return false for every row, so SeedKeys
// persisted zero keys and logged nothing at all. The fixture renames the header
// while keeping the rows it is meant to select, so a silent run still seeds
// zero keys and only the specific missing-column report satisfies the test -
// any unrelated error (a malformed fixture, a read failure) fails it instead
// of masquerading as success.
func TestSeedKeysMissingRegisterColumnIsLoud(t *testing.T) {
	db, err := database.New(database.Options{Engine: database.SQLITE, Filename: ":memory:"})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	log, sink := capturingTestLogger(t)
	krh, err := NewKeysRegistrationHelper(&db, log)
	if err != nil {
		t.Fatalf("build keys helper: %v", err)
	}

	// Every record carries the same field count as server/permissions/keys.csv
	// (title row, header row, data rows): Go's encoding/csv locks the field
	// count to the first record, so a stray comma would abort parsing early
	// with an unrelated read error.
	rows := [][]string{
		{"", "", "", "Authorization", "", "", "", "", "", "", "", "Keychain", "Keys", "", ""},
		{"Category", "Function", "Feature", "User", "Team Admin", "Academy Admin", "Leaner", "Workspace Admin", "Org Billing Manager", "Org Admin", "Provider Admin", "Keychain ID", "Key ID", "Inserted", "LocalProvider"},
		{"Catalog", "Share Design", "Share design", "X", "X", "", "", "X", "", "X", "X", "Catalog Management", "d9ae2b08-762f-418f-916f-43de736b53e2", "X", "TRUE"},
		{"Designs", "Create new design", "Create new Meshery design", "X", "X", "", "", "X", "", "X", "X", "Catalog Management", "14bd933e-83b7-464d-9a4d-d8c8eb9682ab", "X", "TRUE"},
	}
	var fixture strings.Builder
	for _, row := range rows {
		fixture.WriteString(strings.Join(row, ","))
		fixture.WriteString("\n")
	}
	keysPath := filepath.Join(t.TempDir(), "keys.csv")
	if err := os.WriteFile(keysPath, []byte(fixture.String()), 0o644); err != nil {
		t.Fatalf("write keys fixture: %v", err)
	}

	krh.SeedKeys(keysPath)

	// The renamed header must be reported under its own code: asserting on the
	// specific error keeps a malformed fixture (or any other fault) from
	// passing for the wrong reason.
	if !sink.reports(t, ErrKeysRegisterColumnMissingCode) {
		t.Fatalf("a renamed register column was not reported as %s; emitted: %+v", ErrKeysRegisterColumnMissingCode, sink.records(t))
	}

	// The symptom the report explains: with the header gone no row can be
	// selected, so the table stays empty. The expectation is derived from the
	// fixture (its two TRUE rows are skipped), not from a count of shipped keys.
	var seeded int64
	if err := db.Model(&Key{}).Count(&seeded).Error; err != nil {
		t.Fatalf("count seeded keys: %v", err)
	}
	if seeded != 0 {
		t.Fatalf("expected zero keys seeded with a missing register column, got %d", seeded)
	}
}

// TestSeedKeysIntactRegisterColumnSeedsWithoutReport is the mirror image: with
// the register header present, the same-shaped fixture seeds its TRUE rows and
// reports nothing. It pins that the missing-column report fires only for the
// missing header, and that the fix did not break the normal boot path. The
// seeded count is derived from the fixture, not from shipped keys.
func TestSeedKeysIntactRegisterColumnSeedsWithoutReport(t *testing.T) {
	db, err := database.New(database.Options{Engine: database.SQLITE, Filename: ":memory:"})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	log, sink := capturingTestLogger(t)
	krh, err := NewKeysRegistrationHelper(&db, log)
	if err != nil {
		t.Fatalf("build keys helper: %v", err)
	}

	rows := [][]string{
		{"", "", "", "Authorization", "", "", "", "", "", "", "", "Keychain", "Keys", "", ""},
		{"Category", "Function", "Feature", "User", "Team Admin", "Academy Admin", "Leaner", "Workspace Admin", "Org Billing Manager", "Org Admin", "Provider Admin", "Keychain ID", "Key ID", "Inserted", "Local Provider"},
		{"Catalog", "Share Design", "Share design", "X", "X", "", "", "X", "", "X", "X", "Catalog Management", "d9ae2b08-762f-418f-916f-43de736b53e2", "X", "TRUE"},
		{"Designs", "Create new design", "Create new Meshery design", "X", "X", "", "", "X", "", "X", "X", "Catalog Management", "14bd933e-83b7-464d-9a4d-d8c8eb9682ab", "X", "TRUE"},
	}
	var fixture strings.Builder
	for _, row := range rows {
		fixture.WriteString(strings.Join(row, ","))
		fixture.WriteString("\n")
	}
	keysPath := filepath.Join(t.TempDir(), "keys.csv")
	if err := os.WriteFile(keysPath, []byte(fixture.String()), 0o644); err != nil {
		t.Fatalf("write keys fixture: %v", err)
	}

	krh.SeedKeys(keysPath)

	if sink.reports(t, ErrKeysRegisterColumnMissingCode) {
		t.Fatalf("an intact register column was reported as %s; emitted: %+v", ErrKeysRegisterColumnMissingCode, sink.records(t))
	}
	// At least one of the fixture's TRUE rows must have seeded. The lower
	// bound - not an exact count - is deliberate: the row channel holds one
	// row, so the parse goroutine cannot reach EOF and close Done until the
	// select loop has received every row but the last, which makes all-but-last
	// rows deterministic while the final buffered row still races Done. That
	// last-row race is the sibling drain-race work's scope, excluded here by
	// BOUNDARIES, so this test must not depend on winning it.
	var seeded int64
	if err := db.Model(&Key{}).Count(&seeded).Error; err != nil {
		t.Fatalf("count seeded keys: %v", err)
	}
	if seeded < 1 {
		t.Fatalf("expected at least one of the fixture's TRUE rows seeded, got %d", seeded)
	}
}
