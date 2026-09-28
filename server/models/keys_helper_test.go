package models

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/gofrs/uuid"
	"github.com/meshery/meshkit/database"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"io"
)

// captureLogger records errors passed to Error so tests can assert that a
// seeding fault was surfaced instead of silently dropped.
type captureLogger struct {
	mu   sync.Mutex
	errs []error
}

func (l *captureLogger) Error(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errs = append(l.errs, err)
}

func (l *captureLogger) Info(_ ...interface{})                {}
func (l *captureLogger) Infof(_ string, _ ...interface{})     {}
func (l *captureLogger) Debug(_ ...interface{})               {}
func (l *captureLogger) Debugf(_ string, _ ...interface{})    {}
func (l *captureLogger) Warn(_ error)                         {}
func (l *captureLogger) Warnf(_ string, _ ...interface{})     {}
func (l *captureLogger) Errorf(_ string, _ ...interface{})    {}
func (l *captureLogger) Fatal(_ error)                        {}
func (l *captureLogger) Fatalf(_ string, _ ...interface{})    {}
func (l *captureLogger) SetLevel(_ logrus.Level)              {}
func (l *captureLogger) GetLevel() logrus.Level               { return logrus.ErrorLevel }
func (l *captureLogger) UpdateLogOutput(_ io.Writer)          {}
func (l *captureLogger) UpdateErrorLogOutput(_ io.Writer)     {}
func (l *captureLogger) ControllerLogger() logr.Logger        { return logr.Discard() }
func (l *captureLogger) DatabaseLogger() gormlogger.Interface { return nil }

func (l *captureLogger) errorCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.errs)
}

// newKeysSeedDB opens an isolated in-memory database. The DSN names a
// shared-cache database unique to the caller: a bare ":memory:" gives every
// pooled connection its own empty database, so a migrate on one connection
// is invisible to a save on another.
var keysSeedDBSeq int32

func newKeysSeedDB(t *testing.T, name string) (*gorm.DB, *database.Handler) {
	t.Helper()
	// The sequence number keeps the database fresh across -count repetitions
	// of the test within one process: rows left behind by an earlier
	// iteration would turn the next iteration's saves into updates, and the
	// gating create callbacks below would never fire.
	seq := atomic.AddInt32(&keysSeedDBSeq, 1)
	dsn := fmt.Sprintf("file:%s-%d?mode=memory&cache=shared", name, seq)
	gdb, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err, "open in-memory database")
	return gdb, &database.Handler{DB: gdb}
}

func keysSeedHelper(t *testing.T, gdb *gorm.DB, clog *captureLogger) *KeysRegistrationHelper {
	t.Helper()
	krh, err := NewKeysRegistrationHelper(&database.Handler{DB: gdb}, clog)
	require.NoError(t, err, "build keys registration helper")
	return krh
}

// parserGone polls the goroutine dump until no goroutine remains inside the
// meshkit CSV parser, which proves Parse has returned and therefore run its
// deferred cancel. It reports whether the parser exited before the deadline.
func parserGone(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	var buf [1 << 20]byte
	for time.Now().Before(deadline) {
		n := runtime.Stack(buf[:], true)
		if !strings.Contains(string(buf[:n]), "meshkit/utils/csv") {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// runSeedKeys runs SeedKeys off the test goroutine and reports whether it
// returned before the timeout. It cannot catch an escaping panic (that lands
// on the spawned goroutine), so tests covering the panic strand call SeedKeys
// directly instead.
func runSeedKeys(t *testing.T, krh *KeysRegistrationHelper, fixture string, timeout time.Duration) bool {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		krh.SeedKeys(fixture)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func mustKeyUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.FromString(s)
	require.NoError(t, err, "parse fixture key id")
	return id
}

func seededKeyIDs(t *testing.T, gdb *gorm.DB) map[uuid.UUID]bool {
	t.Helper()
	var keys []Key
	require.NoError(t, gdb.Find(&keys).Error, "list seeded keys")
	ids := make(map[uuid.UUID]bool, len(keys))
	for _, k := range keys {
		ids[k.ID] = true
	}
	return ids
}

// TestSeedKeysDrainsBufferedRowAndErrorAfterCancel forces the
// cancellation-before-drain ordering: the first save blocks until the parser
// has provably exited (cancel runs only as Parse returns, so at release time
// a row sits buffered in ch, an error sits buffered in errorChan, and the
// context is already closed). The consumer must persist the row and surface
// the error instead of dropping whichever the select did not pick.
func TestSeedKeysDrainsBufferedRowAndErrorAfterCancel(t *testing.T) {
	gdb, _ := newKeysSeedDB(t, "seed-drain")
	clog := &captureLogger{}

	started := make(chan struct{})
	release := make(chan struct{})
	var saves int32
	require.NoError(t, gdb.Callback().Create().Before("gorm:create").Register("test:gate-first-save", func(db *gorm.DB) {
		if atomic.AddInt32(&saves, 1) == 1 {
			close(started)
			<-release
		}
	}), "register gating create callback")

	krh := keysSeedHelper(t, gdb, clog)

	done := make(chan struct{})
	go func() {
		defer close(done)
		krh.SeedKeys(filepath.Join("testdata", "keys_seed_drain.csv"))
	}()

	select {
	case <-started:
	case <-time.After(30 * time.Second):
		t.Fatal("first key save never started")
	}

	// The fixture holds exactly one more valid row and one row that fails to
	// unmarshal after the gated save, then only FALSE rows: the parser can
	// buffer both without ever blocking, so it must reach EOF, cancel, and
	// exit while the consumer is still gated.
	require.True(t, parserGone(30*time.Second), "parser did not exit while the first save was gated")
	close(release)

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("SeedKeys did not return after the gate was released")
	}

	ids := seededKeyIDs(t, gdb)
	assert.True(t, ids[mustKeyUUID(t, "11111111-1111-4111-8111-111111111111")], "first row was not persisted")
	assert.True(t, ids[mustKeyUUID(t, "22222222-2222-4222-8222-222222222222")], "last row buffered at cancel time was dropped")
	assert.Len(t, ids, 2, "unexpected keys persisted")
	assert.GreaterOrEqual(t, clog.errorCount(), 1, "parse error buffered at cancel time was dropped unlogged")
}

// TestSeedKeysContainsPanickingSave panics inside the persistence of the first
// row while two more valid rows follow. The panic must not escape the seed
// loop: otherwise nobody drains the channels, the parser blocks forever on
// its next send, and the file it holds is never released.
func TestSeedKeysContainsPanickingSave(t *testing.T) {
	gdb, _ := newKeysSeedDB(t, "seed-panic")
	clog := &captureLogger{}

	require.NoError(t, gdb.Callback().Create().Before("gorm:create").Register("test:panic-row", func(db *gorm.DB) {
		if k, ok := db.Statement.Dest.(*Key); ok && k.Function == "panic-row" {
			panic("test-induced save panic")
		}
	}), "register panicking create callback")

	krh := keysSeedHelper(t, gdb, clog)

	var fixtureAbs string
	if runtime.GOOS == "linux" {
		var err error
		fixtureAbs, err = filepath.Abs(filepath.Join("testdata", "keys_seed_panic.csv"))
		require.NoError(t, err, "resolve fixture path")
	}

	// SeedKeys blocks until the loop exits, so call it directly: a panic that
	// escapes the loop then unwinds this goroutine, where the recover below
	// observes it instead of crashing the test binary.
	seedDone := make(chan struct{})
	go func() {
		select {
		case <-seedDone:
		case <-time.After(60 * time.Second):
			t.Error("SeedKeys did not return")
		}
	}()
	panicked := func() (escaped bool) {
		defer func() {
			if r := recover(); r != nil {
				escaped = true
			}
		}()
		krh.SeedKeys(filepath.Join("testdata", "keys_seed_panic.csv"))
		return false
	}()
	close(seedDone)
	assert.False(t, panicked, "a SaveUsersKey panic escaped SeedKeys instead of being contained in the seed loop")

	ids := seededKeyIDs(t, gdb)
	assert.True(t, ids[mustKeyUUID(t, "66666666-6666-4666-8666-666666666666")], "row after the panicking save was not persisted")
	assert.True(t, ids[mustKeyUUID(t, "77777777-7777-4777-8777-777777777777")], "last row was not persisted")
	assert.Len(t, ids, 2, "unexpected keys persisted")
	assert.GreaterOrEqual(t, clog.errorCount(), 1, "contained panic was not reported")

	assert.True(t, parserGone(30*time.Second), "parser goroutine is still blocked on send: nobody is draining its channels")
	if runtime.GOOS == "linux" {
		assert.True(t, waitForFixtureFDClosed(t, fixtureAbs, 15*time.Second), "parser file handle was not released")
	}
}

// fixtureFDOpen reports whether any open file descriptor in this process
// points at the given absolute path. It names the leaked file instead of
// comparing total descriptor counts, which unrelated runtime activity can
// shift by one in either direction.
func fixtureFDOpen(t *testing.T, absPath string) bool {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	require.NoError(t, err, "list open file descriptors")
	for _, e := range entries {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name()))
		if err != nil {
			continue
		}
		if target == absPath || strings.HasPrefix(target, absPath+" ") {
			return true
		}
	}
	return false
}

// waitForFixtureFDClosed polls until the file is no longer held open. The
// parser never closes it explicitly; the handle is released when the parser
// goroutine finishes, the reader becomes unreachable, and the os.File
// finalizer runs, so each poll forces a collection cycle first.
func waitForFixtureFDClosed(t *testing.T, absPath string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		runtime.GC()
		if !fixtureFDOpen(t, absPath) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fixtureFDOpen(t, absPath) == false
}

// TestSeedKeysPersistsAllShippedKeys seeds from the shipped permissions table
// end to end and pins the defect's concrete symptom: the last matching row,
// the View Credentials capability key, must be present along with every other
// registered key, and unregistered rows must stay out.
func TestSeedKeysPersistsAllShippedKeys(t *testing.T) {
	gdb, _ := newKeysSeedDB(t, "seed-shipped")
	clog := &captureLogger{}
	krh := keysSeedHelper(t, gdb, clog)

	require.True(t, runSeedKeys(t, krh, filepath.Join("..", "permissions", "keys.csv"), 60*time.Second), "SeedKeys did not return")

	var count int64
	require.NoError(t, gdb.Model(&Key{}).Count(&count).Error, "count seeded keys")
	assert.Equal(t, int64(68), count, "seeded key count changed: update the expectation if keys.csv gained or lost Local Provider rows")

	ids := seededKeyIDs(t, gdb)
	assert.True(t, ids[mustKeyUUID(t, "96759f76-4add-45f8-b4ef-d4ace5ab1bc4")], "View Credentials key (last matching row) is missing")
	assert.False(t, ids[mustKeyUUID(t, "045fad17-d2cc-46e8-bb10-f9ee026c799f")], "FALSE row was seeded but must not be")
}
