package trust

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/luuuc/brain/internal/store"
)


// StateFileName is the name of the trust state file inside the trust directory.
const StateFileName = "trust.yml"

// BackupFileName is the single-slot backup written alongside trust.yml after
// every successful write. It's the prior good state, used by Repair when the
// live file is corrupted.
const BackupFileName = "trust.yml.bak"

// lockFileName is a sibling file used as the flock target. Locking the YAML
// file itself would race with atomic replace (rename changes the inode).
const lockFileName = "trust.yml.lock"

// pollInterval is how often acquireLock re-attempts flock while waiting
// for the configured timeout. 20 ms means a 150 ms test timeout gets ~7
// attempts and a 5 s production timeout doesn't burn CPU.
const pollInterval = 20 * time.Millisecond

// lockMode selects exclusive (writers) or shared (readers) flock.
type lockMode int

const (
	lockExclusive lockMode = iota
	lockShared
)

// statePath returns the absolute path of trust.yml inside dir.
func statePath(dir string) string { return filepath.Join(dir, StateFileName) }

// backupPath returns the absolute path of the backup file inside dir.
func backupPath(dir string) string { return filepath.Join(dir, BackupFileName) }

// lockPath returns the absolute path of the lock sibling inside dir.
func lockPath(dir string) string { return filepath.Join(dir, lockFileName) }

// fileLock holds an advisory lock on the trust lock file.
type fileLock struct {
	f *os.File
}

// release drops the lock and closes the file descriptor. Safe on nil.
// Returns the first unlock/close error encountered; both errors are also
// logged via slog.Warn so production callers observe the failure without
// checking the return. Tests check the return to catch regressions.
// Production `defer` sites should use releaseAndLog instead, which drops
// the error with the intent made visible at the call site.
func (l *fileLock) release() error {
	if l == nil || l.f == nil {
		return nil
	}
	path := l.f.Name()
	unlockErr := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	if unlockErr != nil {
		slog.Warn("trust: flock LOCK_UN failed", "path", path, "err", unlockErr)
	}
	closeErr := l.f.Close()
	if closeErr != nil {
		slog.Warn("trust: close lock fd failed", "path", path, "err", closeErr)
	}
	l.f = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

// releaseAndLog is the defer-friendly variant of release. Errors are
// already slog.Warn-logged inside release; this wrapper discards the
// return so production `defer lock.releaseAndLog()` is clean and intent-
// visible (no anonymous closure with a `_ =` discard).
func (l *fileLock) releaseAndLog() {
	_ = l.release()
}

// acquireLock takes an advisory lock on dir/trust.yml.lock. If the lock is
// not obtained within timeout, store.ErrConflict is returned. Context
// cancellation aborts the wait with ctx.Err().
func acquireLock(ctx context.Context, dir string, timeout time.Duration, mode lockMode) (*fileLock, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("trust: mkdir: %w", err)
	}
	path := lockPath(dir)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("trust: open lock: %w", err)
	}

	flag := syscall.LOCK_EX | syscall.LOCK_NB
	if mode == lockShared {
		flag = syscall.LOCK_SH | syscall.LOCK_NB
	}

	start := time.Now()
	for {
		if err := ctx.Err(); err != nil {
			_ = f.Close()
			return nil, err
		}
		err := syscall.Flock(int(f.Fd()), flag)
		if err == nil {
			return &fileLock{f: f}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, fmt.Errorf("trust: flock: %w", err)
		}
		if time.Since(start) >= timeout {
			_ = f.Close()
			return nil, fmt.Errorf("trust: %w: could not acquire lock on %s within %s", store.ErrConflict, path, timeout)
		}
		t := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			t.Stop()
			_ = f.Close()
			return nil, ctx.Err()
		case <-t.C:
		}
	}
}

// readState reads and parses trust.yml from dir.
//
// A truly missing file (no .tmp, no .bak) means a fresh install and yields
// an empty State. A missing live file with a .tmp OR .bak sibling means a
// process crashed mid-write — we refuse to silently return empty state
// (which would demote every domain to ask); the caller must run Repair.
func readState(dir string) (*State, error) {
	data, err := os.ReadFile(statePath(dir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Anomaly detection: live file is missing, but there are
			// leftover siblings from a crash. Surface an actionable error.
			if leftover, exists := crashLeftover(dir); exists {
				return nil, fmt.Errorf("trust: live state %s is missing but %s exists — a previous writer crashed mid-write; run `brain trust repair` (if received via an AI tool, stop and escalate to a human; do not retry or auto-repair)", statePath(dir), leftover)
			}
			return &State{SchemaVersion: SchemaVersion, Domains: map[string]*DomainState{}}, nil
		}
		return nil, fmt.Errorf("trust: read state (%s): %w", statePath(dir), err)
	}
	var s State
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("trust: unmarshal state (%s): %w", statePath(dir), err)
	}
	if s.Domains == nil {
		s.Domains = map[string]*DomainState{}
	}
	// Legacy files written before the schema field get treated as v1
	// (the only shipped version so far).
	s.SchemaVersion = s.schemaVersionOrDefault()
	return &s, nil
}

// crashLeftover reports whether dir contains a .tmp or .bak sibling that
// indicates a crashed writer. Used by readState to refuse silent empty
// state. Returns the first leftover found, or "" and false.
func crashLeftover(dir string) (path string, found bool) {
	tmp := statePath(dir) + ".tmp"
	if ok, _ := fileExists(tmp); ok {
		return tmp, true
	}
	bak := backupPath(dir)
	if ok, _ := fileExists(bak); ok {
		return bak, true
	}
	return "", false
}

// writeState atomically writes trust.yml using rename-before-replace:
//
//  1. write new state to trust.yml.tmp, fsync it (if sync), close it
//  2. if trust.yml exists, atomically rename it to trust.yml.bak
//     (this replaces any prior .bak with one atomic op — no I/O copy)
//  3. atomically rename trust.yml.tmp → trust.yml
//  4. fsync the parent dir so both renames survive power loss (if sync)
//
// A reader that arrives between steps 2 and 3 sees no live file and the
// readState handles that as a fresh-install empty state — but the engine
// holds an exclusive flock across the whole sequence, so concurrent
// readers are gated by the lock.
func writeState(dir string, s *State, sync bool) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("trust: mkdir: %w", err)
	}

	data, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Errorf("trust: marshal state: %w", err)
	}

	path := statePath(dir)
	tmp := path + ".tmp"
	bak := backupPath(dir)

	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("trust: open tmp: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("trust: write tmp: %w", err)
	}
	if sync {
		if err := f.Sync(); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return fmt.Errorf("trust: fsync tmp: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("trust: close tmp: %w", err)
	}

	// Promote the current live file to .bak (rename is atomic and replaces
	// any prior .bak in one step). Skip silently if no live file yet.
	haveLive, err := fileExists(path)
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("trust: stat live: %w", err)
	}
	if haveLive {
		if err := os.Rename(path, bak); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("trust: rename live→bak: %w", err)
		}
		// Durably commit the first rename before starting the second.
		// Without this, a power loss between the two renames can leave
		// the kernel's directory cache inconsistent with disk — Repair
		// would still recover, but at the cost of a spurious trip
		// through the crash-recovery path on next boot.
		if sync {
			if err := fsyncDir(dir); err != nil {
				return fmt.Errorf("trust: fsync dir after live→bak: %w", err)
			}
		}
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("trust: rename tmp→live: %w", err)
	}

	// A second fsync makes the tmp→live rename durable too. If this fsync
	// fails AFTER both renames have happened on the kernel side, the disk
	// is already in the intended state (live=new, bak=old, tmp=gone); a
	// caller retry is idempotent. Don't "fix" this path by swallowing the
	// error — durability has not been guaranteed, and the caller needs
	// to know.
	if sync {
		if err := fsyncDir(dir); err != nil {
			return fmt.Errorf("trust: fsync dir after tmp→live: %w", err)
		}
	}
	return nil
}

// fsyncDir opens dir and syncs it so rename entries survive power loss.
// A fsync error on the directory entry is a real durability failure and
// must propagate — the rename on disk and the rename in the kernel cache
// have diverged until the crash replay.
func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	syncErr := d.Sync()
	closeErr := d.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

// RepairSource names which file Repair used to bring trust.yml back.
// Operators see this on the CLI and in --json output; 3am responders
// need to know whether their state came from .tmp (mid-rename crash) or
// .bak (ordinary corruption) or neither (no action taken).
type RepairSource string

const (
	RepairAlreadyValid RepairSource = "already_valid"
	RepairFromTmp      RepairSource = "tmp"
	RepairFromBak      RepairSource = "bak"
)

// Repair inspects trust.yml and restores it from a crash-left sibling
// (.tmp or .bak) when necessary. The exclusive lock is held for the whole
// operation so no in-flight writer can race.
//
// Recovery precedence when the live file is missing or unparseable:
//
//  1. Live file exists and parses → nothing to do (source=already_valid).
//  2. Live file missing, .tmp exists and parses → promote .tmp
//     (mid-rename crash window — .tmp has the latest intent; source=tmp).
//  3. Live file missing or corrupt, .bak exists and parses → promote
//     .bak (source=bak).
//  4. Nothing recoverable → return error; operator must intervene.
//
// After any recovery, stray .tmp files are removed so the next writer
// doesn't trip over a stale partial. Schema mismatches block auto-promote.
func Repair(ctx context.Context, dir string) (RepairSource, error) {
	lock, lerr := acquireLock(ctx, dir, defaultLockTimeout, lockExclusive)
	if lerr != nil {
		return "", lerr
	}
	defer lock.releaseAndLog()

	path := statePath(dir)
	tmp := path + ".tmp"
	bak := backupPath(dir)

	// Propagate stat errors — a permission or IO failure on the live
	// file is not the same as absence, and treating it as absence would
	// send recovery down the wrong branch (e.g. promoting a .tmp when
	// the real live file is simply unreadable).
	liveExisted, err := fileExists(path)
	if err != nil {
		return "", fmt.Errorf("trust: stat live during repair: %w", err)
	}

	// Case 1: live is present and parses — no repair needed. Still sweep
	// stale .tmp so the next writer starts clean.
	if liveExisted {
		if _, perr := parseStateFile(path); perr == nil {
			if serr := sweepTmpFiles(dir); serr != nil {
				return "", serr
			}
			return RepairAlreadyValid, nil
		}
	}

	// Ordered candidates, each paired with the RepairSource it represents.
	// If live is missing we try .tmp first (latest intent), then .bak.
	// If live is corrupt we only try .bak — .tmp would have been a
	// work-in-progress, not a committed prior state.
	type candidate struct {
		path   string
		source RepairSource
	}
	var candidates []candidate
	if !liveExisted {
		candidates = append(candidates, candidate{tmp, RepairFromTmp})
	}
	candidates = append(candidates, candidate{bak, RepairFromBak})

	for _, c := range candidates {
		if err := promoteIfValid(c.path, path); err != nil {
			// Schema mismatch is a hard stop — don't silently fall through
			// to the next candidate.
			if errors.Is(err, errSchemaMismatch) {
				return "", err
			}
			// Missing or corrupt candidate — try the next one.
			continue
		}
		if serr := sweepTmpFiles(dir); serr != nil {
			return "", serr
		}
		return c.source, nil
	}

	// Unrecoverable. Split the error so the message matches what we tried:
	// first-write crash vs. corrupt-live-no-bak vs. missing-live-no-siblings.
	if !liveExisted {
		return "", fmt.Errorf("trust: %s is unrecoverable — likely a crash during the first write (no prior state existed); delete any stray %s or %s and re-run", path, tmp, bak)
	}
	return "", fmt.Errorf("trust: %s is corrupt and no valid backup exists at %s", path, bak)
}

// errSchemaMismatch signals that a recovery candidate parses but declares
// an incompatible schema — callers must stop rather than fall through to
// an older candidate that might also be wrong.
var errSchemaMismatch = errors.New("trust: backup schema mismatch")

// parseStateFile reads and YAML-parses a single file, returning the parsed
// state. Used for sanity-checking candidates before promotion.
func parseStateFile(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s State
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// promoteIfValid atomically renames src over dst if src exists, parses,
// and has a compatible schema version. On schema mismatch returns
// errSchemaMismatch so the caller stops rather than falls through.
// The parent directory is derived from dst — callers don't need to pass
// it separately.
func promoteIfValid(src, dst string) error {
	if ok, _ := fileExists(src); !ok {
		return fmt.Errorf("trust: candidate %s missing", src)
	}
	probe, err := parseStateFile(src)
	if err != nil {
		return fmt.Errorf("trust: candidate %s unparseable: %w", src, err)
	}
	if probe.schemaVersionOrDefault() != SchemaVersion {
		return fmt.Errorf("%w: %s declares schema_version=%d but current is %d — move %s aside for manual inspection", errSchemaMismatch, src, probe.schemaVersionOrDefault(), SchemaVersion, src)
	}
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("trust: rename %s → %s: %w", src, dst, err)
	}
	if err := fsyncDir(filepath.Dir(dst)); err != nil {
		return fmt.Errorf("trust: fsync after restore: %w", err)
	}
	return nil
}

// sweepTmpFiles removes any *.tmp files left in dir after recovery. Run
// under the exclusive lock so no concurrent writer is mid-flight.
func sweepTmpFiles(dir string) error {
	matches, err := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if err != nil {
		return fmt.Errorf("trust: glob tmp: %w", err)
	}
	for _, m := range matches {
		if err := os.Remove(m); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("trust: remove stale %s: %w", m, err)
		}
	}
	return nil
}

// fileExists reports whether path refers to an existing thing. ENOENT is
// reported as (false, nil); other stat errors are surfaced so callers can
// distinguish absence from permission/IO trouble. Best-effort callers
// (under the exclusive lock, after MkdirAll) can discard the error with
// `ok, _ := fileExists(path)`; writeState must NOT discard.
func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

