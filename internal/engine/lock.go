package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/luuuc/brain/internal/store"
)

// effectivenessLockFile is the sibling file flocked to serialise Track
// writes. Locking the .md files directly would race with atomic replace
// (rename changes the inode — same rationale as trust's lock).
const effectivenessLockFile = ".lock"

// effectivenessLockDir is the subdir under the brain root that holds the
// effectiveness memories and their sibling lock.
const effectivenessLockDir = "effectiveness"

// defaultEngineLockTimeout matches trust's 5-second ceiling so operators
// see the same "took too long to acquire" behaviour across both engines.
const defaultEngineLockTimeout = 5 * time.Second

// engineLockPoll is how often we re-try flock while waiting. 20 ms echoes
// the trust engine's choice.
const engineLockPoll = 20 * time.Millisecond

// lockMode selects exclusive (writers) or shared (readers) flock.
type lockMode int

const (
	lockExclusive lockMode = iota
	lockShared
)

// engineLock ties an in-process mutex and (optionally) a cross-process
// flock to the same lifetime, so a single releaseAndLog unwinds both in
// the right order (file first, then mutex — inverse of acquisition).
type engineLock struct {
	f  *os.File // nil when the engine has no lockDir (in-process-only mode)
	mu *sync.Mutex
}

// releaseAndLog drops the file lock and unlocks the in-process mutex.
// Errors are logged via slog and discarded so production sites can use
// `defer lock.releaseAndLog()` without wrapping.
func (l *engineLock) releaseAndLog() {
	if l == nil {
		return
	}
	if l.f != nil {
		path := l.f.Name()
		if err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN); err != nil {
			slog.Warn("engine: flock LOCK_UN failed", "path", path, "err", err)
		}
		if err := l.f.Close(); err != nil {
			slog.Warn("engine: close lock fd failed", "path", path, "err", err)
		}
	}
	if l.mu != nil {
		l.mu.Unlock()
	}
}

// acquireEffectivenessLock serialises effectiveness verbs. Exclusive for
// writers (Track), shared for readers (EffectivenessStatsFor and
// loadEffectivenessScores). Always takes e.effMu first; if a lockDir was
// configured, then takes a syscall.Flock on the sibling lockfile. Returns
// store.ErrConflict on timeout so callers can map it uniformly.
//
// The returned lock owns both concerns; panics or errors during
// acquisition release the mutex deterministically via a locked-flag
// deferred cleanup — no in-flight goroutine can leak e.effMu even if the
// kernel raises a runtime panic between Lock and return.
func (e *Engine) acquireEffectivenessLock(ctx context.Context, mode lockMode) (*engineLock, error) {
	e.effMu.Lock()

	// Any return path that doesn't hand the mutex to a returned
	// engineLock must unlock. `transferred` flips to true at the exact
	// moment the success path's return-value captures the mutex, so a
	// panic anywhere before that point still unwinds e.effMu cleanly.
	transferred := false
	defer func() {
		if !transferred {
			e.effMu.Unlock()
		}
	}()

	if e.lockDir == "" {
		lock := &engineLock{mu: &e.effMu}
		transferred = true
		return lock, nil
	}

	dir := filepath.Join(e.lockDir, effectivenessLockDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("engine: mkdir effectiveness: %w", err)
	}
	path := filepath.Join(dir, effectivenessLockFile)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("engine: open lock: %w", err)
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
			lock := &engineLock{f: f, mu: &e.effMu}
			transferred = true
			return lock, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, fmt.Errorf("engine: flock: %w", err)
		}
		if time.Since(start) >= e.lockTimeout {
			_ = f.Close()
			return nil, fmt.Errorf("engine: %w: could not acquire effectiveness lock on %s within %s (another brain process may be holding it; try `lsof %s` or retry)",
				store.ErrConflict, path, e.lockTimeout, path)
		}
		t := time.NewTimer(engineLockPoll)
		select {
		case <-ctx.Done():
			t.Stop()
			_ = f.Close()
			return nil, ctx.Err()
		case <-t.C:
		}
	}
}
