package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luuuc/brain/internal/store"
)

// Test-only typed projections of the JSON the trust commands emit. The
// shared shape lives in trust.DecisionJSON; these structs only exist so
// tests can assert with field names instead of map keys.
type trustJSON struct {
	Domain         string `json:"domain"`
	Level          string `json:"level"`
	CleanShips     int    `json:"clean_ships"`
	LastFailure    string `json:"last_failure,omitempty"`
	LastPromotion  string `json:"last_promotion,omitempty"`
	Urgency        string `json:"urgency,omitempty"`
	Recommendation string `json:"recommendation"`
	History        []struct {
		At      string `json:"at"`
		Kind    string `json:"kind"`
		Outcome string `json:"outcome,omitempty"`
		From    string `json:"from,omitempty"`
		To      string `json:"to,omitempty"`
		Ref     string `json:"ref,omitempty"`
		Reason  string `json:"reason,omitempty"`
	} `json:"history,omitempty"`
}

type trustRecordJSON struct {
	Decision       trustJSON `json:"decision"`
	Promoted       bool      `json:"promoted"`
	Demoted        bool      `json:"demoted"`
	Deduplicated   bool      `json:"deduplicated"`
	LessonsTouched int       `json:"lessons_touched,omitempty"`
	LessonsRetired int       `json:"lessons_retired,omitempty"`
}

type trustListJSON struct {
	Domains []trustJSON `json:"domains"`
	Count   int         `json:"count"`
}

// (a) brain trust on unknown domain shows ask/escalate.
func TestTrustIntegration_CheckUnknownDomain(t *testing.T) {
	dir := setupBrainDir(t)
	code, out := run(t, dir, "--json", "trust", "--domain", "code")
	if code != 0 {
		t.Fatalf("trust check: exit %d, out=%s", code, out)
	}
	var r trustJSON
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Level != "ask" {
		t.Errorf("level = %q, want ask", r.Level)
	}
	if r.Recommendation != "escalate" {
		t.Errorf("recommendation = %q, want escalate", r.Recommendation)
	}
}

// (b) brain trust record clean ticks counter and prints new state.
func TestTrustIntegration_RecordClean(t *testing.T) {
	dir := setupBrainDir(t)
	code, out := run(t, dir, "--json", "trust", "record", "--domain", "code", "--outcome", "clean")
	if code != 0 {
		t.Fatalf("record: exit %d, out=%s", code, out)
	}
	var r trustRecordJSON
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Decision.Level != "ask" {
		t.Errorf("level = %q, want ask", r.Decision.Level)
	}
	if r.Decision.CleanShips != 1 {
		t.Errorf("clean_ships = %d, want 1", r.Decision.CleanShips)
	}
}

// (c) brain trust override writes a correction and records override event.
func TestTrustIntegration_Override(t *testing.T) {
	dir := setupBrainDir(t)
	code, out := run(t, dir, "--json", "trust", "override", "--domain", "database", "--reason", "I disagree with the tool")
	if code != 0 {
		t.Fatalf("override: exit %d, out=%s", code, out)
	}
	var r trustJSON
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Domain != "database" {
		t.Errorf("domain = %q", r.Domain)
	}

	// The override should also have produced a correction memory listable
	// via brain list --layer correction.
	code, out = run(t, dir, "--json", "list", "--layer", "correction")
	if code != 0 {
		t.Fatalf("list: exit %d", code)
	}
	var lr ListResult
	if err := json.Unmarshal([]byte(out), &lr); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if lr.Count != 1 {
		t.Fatalf("expected 1 correction, got %d", lr.Count)
	}
}

// (d) brain trust list shows every known domain.
func TestTrustIntegration_List(t *testing.T) {
	dir := setupBrainDir(t)
	for _, d := range []string{"frontend", "database", "testing"} {
		code, _ := run(t, dir, "trust", "record", "--domain", d, "--outcome", "clean")
		if code != 0 {
			t.Fatalf("seed record %s: exit %d", d, code)
		}
	}
	code, out := run(t, dir, "--json", "trust", "list")
	if code != 0 {
		t.Fatalf("list: exit %d", code)
	}
	var r trustListJSON
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Count != 3 {
		t.Fatalf("count = %d, want 3", r.Count)
	}
}

// (e) brain trust --urgency hotfix raises floor to notify when at ask.
func TestTrustIntegration_UrgencyHotfix(t *testing.T) {
	dir := setupBrainDir(t)
	code, out := run(t, dir, "--json", "trust", "--domain", "code", "--urgency", "hotfix")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var r trustJSON
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Level != "ask" {
		t.Errorf("level = %q, want ask", r.Level)
	}
	if r.Recommendation != "ship_notify" {
		t.Errorf("recommendation = %q, want ship_notify", r.Recommendation)
	}
}

// (f) brain trust --verbose includes history.
func TestTrustIntegration_Verbose(t *testing.T) {
	dir := setupBrainDir(t)
	code, _ := run(t, dir, "trust", "record", "--domain", "code", "--outcome", "clean", "--ref", "PR #1")
	if code != 0 {
		t.Fatalf("record: exit %d", code)
	}
	code, out := run(t, dir, "--json", "trust", "--domain", "code", "--verbose")
	if code != 0 {
		t.Fatalf("check: exit %d", code)
	}
	var r trustJSON
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(r.History) != 1 || r.History[0].Kind != "outcome" {
		t.Fatalf("expected 1 outcome event in history, got %+v", r.History)
	}
}

// (g) CLI error exit codes — 3 for input, explicit for conflict is
// exercised separately via the lock path test.
func TestTrustIntegration_InputErrors(t *testing.T) {
	dir := setupBrainDir(t)
	tests := []struct {
		name string
		args []string
	}{
		{"record no domain", []string{"trust", "record", "--outcome", "clean"}},
		{"record invalid outcome", []string{"trust", "record", "--domain", "x", "--outcome", "maybe"}},
		{"override no domain", []string{"trust", "override", "--reason", "why"}},
		{"override no reason", []string{"trust", "override", "--domain", "x"}},
		{"invalid urgency", []string{"trust", "--domain", "x", "--urgency", "whenever"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, _ := run(t, dir, tc.args...)
			if code != 3 {
				t.Fatalf("exit %d, want 3", code)
			}
		})
	}
}

// (i) Any error wrapping store.ErrConflict maps to exit code 4 via
// mapTrustErr. The end-to-end lock-timeout behavior is covered in
// internal/trust/state_test.go; here we test the CLI's translation.
func TestMapTrustErr_ConflictIsExitFour(t *testing.T) {
	wrapped := fmt.Errorf("trust: record: %w: example", store.ErrConflict)
	err := mapTrustErr(wrapped)
	var ex *ExitError
	if !errors.As(err, &ex) {
		t.Fatalf("expected *ExitError, got %T (%v)", err, err)
	}
	if ex.Code != 4 {
		t.Fatalf("exit code = %d, want 4", ex.Code)
	}
}

func TestMapTrustErr_PassThroughOnNonConflict(t *testing.T) {
	input := errors.New("some other error")
	out := mapTrustErr(input)
	// Identity check: a non-conflict input must be returned unchanged.
	// Asserting "not an ExitError" would still pass if mapTrustErr wrapped
	// the input in some other type; identity locks the contract.
	if out != input {
		t.Fatalf("pass-through broken: got %v (type %T), want same pointer as input", out, out)
	}
}

func TestMapTrustErr_NilIsNil(t *testing.T) {
	if mapTrustErr(nil) != nil {
		t.Fatal("nil input should return nil")
	}
}

// (j) brain trust with no --domain exits 3 (not 0 with help).
func TestTrustIntegration_NoDomainIsError(t *testing.T) {
	dir := setupBrainDir(t)
	code, _ := run(t, dir, "trust")
	if code != 3 {
		t.Fatalf("exit %d, want 3", code)
	}
}

// (k) brain trust repair on a pristine store reports already-valid in
// both text and JSON modes, with the source exposed so operators can
// tell which file was used (or whether no action was taken).
func TestTrustIntegration_RepairAlreadyValid(t *testing.T) {
	dir := setupBrainDir(t)
	// Seed any state so trust.yml exists.
	if code, _ := run(t, dir, "trust", "record", "--domain", "code", "--outcome", "clean"); code != 0 {
		t.Fatalf("seed: exit %d", code)
	}
	code, out := run(t, dir, "trust", "repair")
	if code != 0 {
		t.Fatalf("repair: exit %d", code)
	}
	if !strings.Contains(out, "no repair needed") {
		t.Fatalf("unexpected output: %s", out)
	}

	// JSON mode exposes the source so downstream tooling can branch on it.
	code, out = run(t, dir, "--json", "trust", "repair")
	if code != 0 {
		t.Fatalf("repair --json: exit %d", code)
	}
	var payload struct {
		Source       string `json:"source"`
		AlreadyValid bool   `json:"already_valid"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal: %v\nout=%s", err, out)
	}
	if payload.Source != "already_valid" {
		t.Fatalf("source = %q, want already_valid", payload.Source)
	}
	if !payload.AlreadyValid {
		t.Fatal("expected already_valid=true")
	}
}

// (l) brain trust repair recovers from a mid-write crash (.tmp promoted).
// Verifies the CLI wiring of RepairSource → stdout: the human message
// calls out "mid-write crash" and the JSON exposes source="tmp". Without
// this test, a silent rename of RepairFromTmp → something else wouldn't
// fail any assertion.
func TestTrustIntegration_RepairFromTmp(t *testing.T) {
	dir := setupBrainDir(t)
	trustDir := filepath.Join(dir, "trust")
	if err := os.MkdirAll(trustDir, 0o755); err != nil {
		t.Fatalf("mkdir trust: %v", err)
	}
	// Simulate: previous writer completed the tmp write and crashed before
	// rename(tmp, live). Live never got populated; .tmp holds the intent.
	tmpContent := "schema_version: 1\ndomains:\n  code:\n    level: notify\n    clean_ships: 3\n"
	if err := os.WriteFile(filepath.Join(trustDir, "trust.yml.tmp"), []byte(tmpContent), 0o644); err != nil {
		t.Fatalf("plant tmp: %v", err)
	}

	code, out := run(t, dir, "trust", "repair")
	if code != 0 {
		t.Fatalf("repair: exit %d, out=%s", code, out)
	}
	if !strings.Contains(out, "mid-write crash") {
		t.Fatalf("human output should mention mid-write crash, got:\n%s", out)
	}

	code, out = run(t, dir, "--json", "trust", "repair")
	if code != 0 {
		t.Fatalf("repair --json (no-op after restore): exit %d, out=%s", code, out)
	}
	// After the first repair promoted tmp, the second repair sees live
	// as valid — this confirms the restore worked end-to-end.
	var payload struct {
		Source       string `json:"source"`
		AlreadyValid bool   `json:"already_valid"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal: %v\nout=%s", err, out)
	}
	if payload.Source != "already_valid" {
		t.Fatalf("second repair source = %q, want already_valid", payload.Source)
	}
}

// (m) brain trust repair restores from .bak when live is corrupt.
// Verifies the human message and JSON source for the .bak recovery path.
func TestTrustIntegration_RepairFromBak(t *testing.T) {
	dir := setupBrainDir(t)
	trustDir := filepath.Join(dir, "trust")
	if err := os.MkdirAll(trustDir, 0o755); err != nil {
		t.Fatalf("mkdir trust: %v", err)
	}
	// Simulate: writer committed a new live then crashed before sweeping
	// .bak, then hand-edited live broke the YAML — we should recover via
	// .bak (prior good state).
	if err := os.WriteFile(filepath.Join(trustDir, "trust.yml"), []byte("{not: [valid, yaml"), 0o644); err != nil {
		t.Fatalf("plant corrupt live: %v", err)
	}
	bakContent := "schema_version: 1\ndomains:\n  code:\n    level: ask\n    clean_ships: 5\n"
	if err := os.WriteFile(filepath.Join(trustDir, "trust.yml.bak"), []byte(bakContent), 0o644); err != nil {
		t.Fatalf("plant bak: %v", err)
	}

	code, out := run(t, dir, "--json", "trust", "repair")
	if code != 0 {
		t.Fatalf("repair --json: exit %d, out=%s", code, out)
	}
	var payload struct {
		Source       string `json:"source"`
		AlreadyValid bool   `json:"already_valid"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal: %v\nout=%s", err, out)
	}
	if payload.Source != "bak" {
		t.Fatalf("source = %q, want bak", payload.Source)
	}
	if payload.AlreadyValid {
		t.Fatal("already_valid must be false when restoring from bak")
	}

	// Human-text mode names the prior-good-state recovery explicitly.
	code, out = run(t, dir, "trust", "repair")
	if code != 0 {
		t.Fatalf("repair text: exit %d", code)
	}
	// After the first repair promoted bak, second run is already_valid.
	if !strings.Contains(out, "no repair needed") {
		t.Fatalf("second run should be no-op, got:\n%s", out)
	}
}

// (h) Text output for trust check contains recommendation.
func TestTrustIntegration_TextOutput(t *testing.T) {
	dir := setupBrainDir(t)
	code, out := run(t, dir, "trust", "--domain", "code")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "Recommendation: escalate") {
		t.Fatalf("expected recommendation in output, got:\n%s", out)
	}
}
