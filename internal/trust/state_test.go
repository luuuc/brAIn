package trust

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/luuuc/brain/internal/store"
)

func TestReadState_missingFileReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	s, err := readState(dir)
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	if len(s.Domains) != 0 {
		t.Fatalf("expected empty domains, got %d", len(s.Domains))
	}
}

func TestWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)

	s := &State{Domains: map[string]*DomainState{
		"database": {
			Level:         LevelNotify,
			CleanShips:    5,
			LastPromotion: &now,
			History: []Event{
				{At: now, Kind: EventPromotion, From: LevelAsk, To: LevelNotify},
			},
		},
	}}
	if err := writeState(dir, s, true); err != nil {
		t.Fatalf("writeState: %v", err)
	}

	got, err := readState(dir)
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	d := got.Domains["database"]
	if d == nil {
		t.Fatalf("database domain missing after round trip")
	}
	if d.Level != LevelNotify || d.CleanShips != 5 {
		t.Fatalf("state not preserved: %+v", d)
	}
	if len(d.History) != 1 || d.History[0].Kind != EventPromotion {
		t.Fatalf("history not preserved: %+v", d.History)
	}
}

// TestAcquireLock_timeoutReturnsErrConflict asserts the sentinel, not wall
// clock. A timing threshold on a 200 ms budget flakes on shared runners.
func TestAcquireLock_timeoutReturnsErrConflict(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	first, err := acquireLock(ctx, dir, time.Second, lockExclusive)
	if err != nil {
		t.Fatalf("first acquireLock: %v", err)
	}
	defer func() { _ = first.release() }()

	_, err = acquireLock(ctx, dir, 150*time.Millisecond, lockExclusive)
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("expected store.ErrConflict, got %v", err)
	}
}

func TestAcquireLock_releaseAllowsReacquire(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	l1, err := acquireLock(ctx, dir, time.Second, lockExclusive)
	if err != nil {
		t.Fatalf("first acquireLock: %v", err)
	}
	if err := l1.release(); err != nil {
		t.Fatalf("l1.release: %v", err)
	}

	l2, err := acquireLock(ctx, dir, time.Second, lockExclusive)
	if err != nil {
		t.Fatalf("second acquireLock after release: %v", err)
	}
	if err := l2.release(); err != nil {
		t.Fatalf("l2.release: %v", err)
	}
}

func TestAcquireLock_ctxCancellationUnblocks(t *testing.T) {
	dir := t.TempDir()
	first, err := acquireLock(context.Background(), dir, time.Second, lockExclusive)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	defer func() { _ = first.release() }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = acquireLock(ctx, dir, 5*time.Second, lockExclusive)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestAcquireLock_sharedAllowsConcurrentReaders(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	r1, err := acquireLock(ctx, dir, time.Second, lockShared)
	if err != nil {
		t.Fatalf("r1: %v", err)
	}
	defer func() { _ = r1.release() }()
	r2, err := acquireLock(ctx, dir, 200*time.Millisecond, lockShared)
	if err != nil {
		t.Fatalf("r2: %v", err)
	}
	defer func() { _ = r2.release() }()
}

func TestAcquireLock_exclusiveBlocksShared(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	w, err := acquireLock(ctx, dir, time.Second, lockExclusive)
	if err != nil {
		t.Fatalf("w: %v", err)
	}
	defer func() { _ = w.release() }()
	_, err = acquireLock(ctx, dir, 150*time.Millisecond, lockShared)
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("expected ErrConflict for reader vs exclusive writer, got %v", err)
	}
}

func TestWriteState_yamlIsParseable(t *testing.T) {
	dir := t.TempDir()
	s := &State{Domains: map[string]*DomainState{
		"code": {Level: LevelAsk, CleanShips: 3},
	}}
	if err := writeState(dir, s, true); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, StateFileName))
	if err != nil {
		t.Fatalf("readfile: %v", err)
	}
	var back State
	if err := yaml.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Domains["code"].CleanShips != 3 {
		t.Fatalf("unexpected round-trip: %+v", back)
	}
}

func TestWriteState_createsBackupOnSecondWrite(t *testing.T) {
	dir := t.TempDir()
	s := &State{Domains: map[string]*DomainState{"code": {Level: LevelAsk}}}
	if err := writeState(dir, s, true); err != nil {
		t.Fatalf("first write: %v", err)
	}
	// No backup yet — first write has no prior state to back up.
	if _, err := os.Stat(filepath.Join(dir, BackupFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup should not exist before second write")
	}

	s.Domains["code"].CleanShips = 1
	if err := writeState(dir, s, true); err != nil {
		t.Fatalf("second write: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, BackupFileName))
	if err != nil {
		t.Fatalf("expected backup to exist: %v", err)
	}
	var back State
	if err := yaml.Unmarshal(data, &back); err != nil {
		t.Fatalf("backup yaml: %v", err)
	}
	if back.Domains["code"].CleanShips != 0 {
		t.Fatalf("backup should be prior state (0), got %d", back.Domains["code"].CleanShips)
	}
}

func TestRepair_noChangeWhenValid(t *testing.T) {
	dir := t.TempDir()
	seed := &State{SchemaVersion: SchemaVersion, Domains: map[string]*DomainState{"code": {Level: LevelNotify, CleanShips: 4}}}
	if err := writeState(dir, seed, true); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(dir, StateFileName))
	if err != nil {
		t.Fatalf("read before: %v", err)
	}

	if _, err := Repair(context.Background(), dir); err != nil {
		t.Fatalf("Repair: %v", err)
	}

	after, err := os.ReadFile(filepath.Join(dir, StateFileName))
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("state changed under no-op repair:\nbefore=%s\nafter=%s", before, after)
	}
}

func TestRepair_restoresFromBackup(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	// First write seeds state. Second write forces a backup.
	if err := writeState(dir, &State{Domains: map[string]*DomainState{"code": {Level: LevelAsk}}}, true); err != nil {
		t.Fatalf("seed 1: %v", err)
	}
	if err := writeState(dir, &State{Domains: map[string]*DomainState{"code": {Level: LevelNotify, CleanShips: 1}}}, true); err != nil {
		t.Fatalf("seed 2: %v", err)
	}
	// Corrupt the live file.
	if err := os.WriteFile(filepath.Join(dir, StateFileName), []byte(":not: :valid: yaml:"), 0o644); err != nil {
		t.Fatalf("corrupt: %v", err)
	}

	source, err := Repair(ctx, dir)
	if err != nil {
		t.Fatalf("Repair: %v", err)
	}
	if source != RepairFromBak {
		t.Fatalf("source = %q, want %q (bak)", source, RepairFromBak)
	}

	restored, err := readState(dir)
	if err != nil {
		t.Fatalf("readState after repair: %v", err)
	}
	// Backup was the prior state (Level ask, before second write).
	if restored.Domains["code"].Level != LevelAsk {
		t.Fatalf("unexpected restored level: %+v", restored.Domains["code"])
	}
}

func TestRepair_refusesSchemaMismatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Live file is corrupt so Repair has to look at the backup.
	if err := os.WriteFile(filepath.Join(dir, StateFileName), []byte("{not: [valid, yaml"), 0o644); err != nil {
		t.Fatalf("corrupt live: %v", err)
	}
	// Backup parses but declares a future schema.
	futureSchema := fmt.Sprintf("schema_version: %d\ndomains: {}\n", SchemaVersion+1)
	if err := os.WriteFile(filepath.Join(dir, BackupFileName), []byte(futureSchema), 0o644); err != nil {
		t.Fatalf("write backup: %v", err)
	}
	_, err := Repair(context.Background(), dir)
	if err == nil {
		t.Fatal("expected error on schema mismatch")
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("error should mention schema_version, got %v", err)
	}
}

// A crash between rename(live→bak) and rename(tmp→live) leaves:
// live missing, tmp has the latest intent, bak has the prior good state.
// readState must refuse to return empty state, and Repair must promote
// tmp (the latest committed intent — already fsync'd before rename).
func TestReadState_anomalyWhenLiveMissingWithTmp(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, StateFileName+".tmp"), []byte("schema_version: 1\ndomains: {}\n"), 0o644); err != nil {
		t.Fatalf("seed tmp: %v", err)
	}
	_, err := readState(dir)
	if err == nil {
		t.Fatal("expected error when live is missing and .tmp exists")
	}
	if !strings.Contains(err.Error(), "repair") {
		t.Fatalf("error should direct operator to repair, got %v", err)
	}
}

func TestReadState_anomalyWhenLiveMissingWithBak(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, BackupFileName), []byte("schema_version: 1\ndomains: {}\n"), 0o644); err != nil {
		t.Fatalf("seed bak: %v", err)
	}
	_, err := readState(dir)
	if err == nil {
		t.Fatal("expected error when live is missing and .bak exists")
	}
}

// Truly fresh install (no live, no tmp, no bak) stays silent — readState
// returns an empty state at the current schema version.
func TestReadState_freshInstallSilent(t *testing.T) {
	dir := t.TempDir()
	s, err := readState(dir)
	if err != nil {
		t.Fatalf("readState fresh: %v", err)
	}
	if s.SchemaVersion != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", s.SchemaVersion, SchemaVersion)
	}
	if len(s.Domains) != 0 {
		t.Fatalf("fresh install should have no domains, got %d", len(s.Domains))
	}
}

// Mid-rename crash recovery: live missing, tmp has latest intent, bak has
// prior good. Repair must promote tmp (newest fsync'd data) over bak.
func TestRepair_recoversFromMidRenameCrash(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Simulate: previous writer got as far as rename(live→bak) then crashed.
	// On disk: bak (old state), tmp (new state), live missing.
	oldState := "schema_version: 1\ndomains:\n  code:\n    level: ask\n    clean_ships: 0\n"
	newState := "schema_version: 1\ndomains:\n  code:\n    level: notify\n    clean_ships: 0\n"
	if err := os.WriteFile(filepath.Join(dir, BackupFileName), []byte(oldState), 0o644); err != nil {
		t.Fatalf("seed bak: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, StateFileName+".tmp"), []byte(newState), 0o644); err != nil {
		t.Fatalf("seed tmp: %v", err)
	}

	source, err := Repair(context.Background(), dir)
	if err != nil {
		t.Fatalf("Repair: %v", err)
	}
	if source != RepairFromTmp {
		t.Fatalf("source = %q, want %q (tmp)", source, RepairFromTmp)
	}

	// Live file must now be the promoted tmp (the newer intent).
	got, err := readState(dir)
	if err != nil {
		t.Fatalf("readState after repair: %v", err)
	}
	if got.Domains["code"].Level != LevelNotify {
		t.Fatalf("expected notify (from tmp), got %q", got.Domains["code"].Level)
	}
	// Stale tmp swept.
	if _, err := os.Stat(filepath.Join(dir, StateFileName+".tmp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".tmp should be swept after repair; stat err = %v", err)
	}
}

// Live missing, only bak present (rename(live→bak) happened, tmp was
// already cleaned or never written). Fall back to bak.
func TestRepair_recoversFromBakWhenTmpAbsent(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, BackupFileName), []byte("schema_version: 1\ndomains:\n  code:\n    level: auto_ship\n    clean_ships: 5\n"), 0o644); err != nil {
		t.Fatalf("seed bak: %v", err)
	}
	if _, err := Repair(context.Background(), dir); err != nil {
		t.Fatalf("Repair: %v", err)
	}
	got, err := readState(dir)
	if err != nil {
		t.Fatalf("readState after repair: %v", err)
	}
	if got.Domains["code"].Level != LevelAutoShip {
		t.Fatalf("expected auto_ship, got %q", got.Domains["code"].Level)
	}
}

// Repair sweeps stale .tmp files even when live is already valid.
func TestRepair_sweepsStaleTmp(t *testing.T) {
	dir := t.TempDir()
	seed := &State{SchemaVersion: SchemaVersion, Domains: map[string]*DomainState{"code": {Level: LevelAsk}}}
	if err := writeState(dir, seed, true); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Plant a stale tmp — SIGKILL-style orphan.
	stale := filepath.Join(dir, StateFileName+".tmp")
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatalf("plant stale: %v", err)
	}

	if _, err := Repair(context.Background(), dir); err != nil {
		t.Fatalf("Repair: %v", err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".tmp should be swept; stat err = %v", err)
	}
}

// First-write-crash scenario: live never existed, both candidates are
// absent or corrupt. The error must name this case explicitly so the
// operator knows the right action is `rm` and re-run, not anguished
// YAML editing.
func TestRepair_firstWriteCrashUnrecoverable(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Only a corrupt .tmp exists — as if fsync never reached disk.
	if err := os.WriteFile(filepath.Join(dir, StateFileName+".tmp"), []byte("{not: [valid, yaml"), 0o644); err != nil {
		t.Fatalf("plant tmp: %v", err)
	}
	_, err := Repair(context.Background(), dir)
	if err == nil {
		t.Fatal("expected unrecoverable error")
	}
	if !strings.Contains(err.Error(), "first write") {
		t.Fatalf("error should identify first-write-crash case, got %v", err)
	}
}

func TestRepair_bothCorruptReturnsError(t *testing.T) {
	dir := t.TempDir()
	// Put unparseable content in both files.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, StateFileName), []byte(":x"), 0o644); err != nil {
		t.Fatalf("corrupt live: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, BackupFileName), []byte(":x"), 0o644); err != nil {
		t.Fatalf("corrupt backup: %v", err)
	}
	_, err := Repair(context.Background(), dir)
	if err == nil {
		t.Fatal("expected error when both files are corrupt")
	}
}
