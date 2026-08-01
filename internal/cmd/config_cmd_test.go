package cmd

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/luuuc/brain/internal/config"
)

func TestConfig_reportsDefaultsWhenNothingIsSet(t *testing.T) {
	clearBrainEnv(t)
	dir := setupBrainDir(t)

	code, out := run(t, dir, "--json", "config")
	if code != 0 {
		t.Fatalf("config: exit %d, out=%s", code, out)
	}
	var res configResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("unmarshal: %v (out=%s)", err, out)
	}

	if len(res.Settings) != len(config.Keys()) {
		t.Fatalf("reported %d settings, want %d", len(res.Settings), len(config.Keys()))
	}
	for _, s := range res.Settings {
		if s.Source != string(config.SourceDefault) {
			t.Errorf("%s reported source %q, want %q", s.Key, s.Source, config.SourceDefault)
		}
	}
}

// The command's whole job: say which layer won for each key.
func TestConfig_attributesEachLayer(t *testing.T) {
	clearBrainEnv(t)
	dir := setupBrainDir(t)
	writeBrainYML(t, dir, "trust:\n  promote_to_notify: 3\nembeddings:\n  model: from-file\n")
	t.Setenv("BRAIN_EMBEDDING_MODEL", "from-env")

	code, out := run(t, dir, "--json", "config")
	if code != 0 {
		t.Fatalf("config: exit %d, out=%s", code, out)
	}
	var res configResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	got := make(map[string]configSetting, len(res.Settings))
	for _, s := range res.Settings {
		got[s.Key] = s
	}

	want := map[string]struct {
		source string
		value  any
	}{
		config.KeyEmbeddingModel: {string(config.SourceEnv), "from-env"},
		config.KeyPromoteNotify:  {string(config.SourceFile), float64(3)},
		config.KeyPromoteFullAuto: {string(config.SourceDefault),
			float64(config.Default().Trust.PromoteToFullAuto)},
	}
	for key, w := range want {
		s, ok := got[key]
		if !ok {
			t.Errorf("%s missing from output", key)
			continue
		}
		if s.Source != w.source {
			t.Errorf("%s source = %q, want %q", key, s.Source, w.source)
		}
		if s.Value != w.value {
			t.Errorf("%s value = %v (%T), want %v", key, s.Value, s.Value, w.value)
		}
	}

	// An env-sourced key must name the variable so the user knows what to unset.
	if got[config.KeyEmbeddingModel].EnvVar != "BRAIN_EMBEDDING_MODEL" {
		t.Errorf("env var not reported: %+v", got[config.KeyEmbeddingModel])
	}
	// A file-only key must not claim an env var exists for it.
	if v := got[config.KeyPromoteNotify].EnvVar; v != "" {
		t.Errorf("%s reported env var %q, want none", config.KeyPromoteNotify, v)
	}
}

// Trust levels appear beside the thresholds, with progress measured against
// the configured value rather than the default.
func TestConfig_showsTrustLevelsAgainstConfiguredThresholds(t *testing.T) {
	clearBrainEnv(t)
	dir := setupBrainDir(t)
	writeBrainYML(t, dir, "trust:\n  promote_to_notify: 4\n")

	if code, out := run(t, dir, "trust", "record", "--domain", "code", "--outcome", "clean"); code != 0 {
		t.Fatalf("record: exit %d, out=%s", code, out)
	}

	code, out := run(t, dir, "--json", "config")
	if code != 0 {
		t.Fatalf("config: exit %d, out=%s", code, out)
	}
	var res configResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(res.Domains) != 1 {
		t.Fatalf("got %d domains, want 1", len(res.Domains))
	}
	d := res.Domains[0]
	if d.Domain != "code" || d.Level != "ask" || d.CleanShips != 1 {
		t.Errorf("domain = %+v, want code/ask/1", d)
	}
	if d.PromoteAt != 4 {
		t.Errorf("PromoteAt = %d, want the configured 4", d.PromoteAt)
	}
}

// A domain at the top of the ladder has nothing left to climb.
func TestConfig_fullAutoDomainHasNoPromoteTarget(t *testing.T) {
	clearBrainEnv(t)
	dir := setupBrainDir(t)
	writeBrainYML(t, dir, "trust:\n  promote_to_notify: 1\n  promote_to_auto_ship: 1\n  promote_to_full_auto: 1\n")

	for i := 0; i < 3; i++ {
		if code, out := run(t, dir, "trust", "record", "--domain", "code", "--outcome", "clean"); code != 0 {
			t.Fatalf("record %d: exit %d, out=%s", i, code, out)
		}
	}

	code, out := run(t, dir, "--json", "config")
	if code != 0 {
		t.Fatalf("config: exit %d, out=%s", code, out)
	}
	var res configResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(res.Domains) != 1 || res.Domains[0].Level != "full_auto" {
		t.Fatalf("domains = %+v, want one at full_auto", res.Domains)
	}
	if res.Domains[0].PromoteAt != 0 {
		t.Errorf("PromoteAt = %d at full_auto, want 0", res.Domains[0].PromoteAt)
	}
}

func TestConfig_textOutput(t *testing.T) {
	clearBrainEnv(t)
	dir := setupBrainDir(t)
	t.Setenv("BRAIN_STORAGE", "markdown")

	code, out := run(t, dir, "config")
	if code != 0 {
		t.Fatalf("config: exit %d, out=%s", code, out)
	}

	for _, want := range []string{
		"Configuration for",
		config.KeyPromoteNotify,
		"(unset)",                 // empty values are marked, not blank
		"env ($BRAIN_STORAGE)",    // env values name their variable
		"no domains recorded yet", // empty trust state is stated, not silent
		"does not demote",         // the asymmetry warning
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// clearBrainEnv unsets every BRAIN_* variable the config package reads, so a
// developer's environment cannot leak into these assertions.
func clearBrainEnv(t *testing.T) {
	t.Helper()
	for _, k := range config.Keys() {
		name := config.EnvVar(k)
		if name == "" {
			continue
		}
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unsetting %s: %v", name, err)
		}
	}
}

// A pg config must stop every command that needs storage, with a message
// naming the file to fix.
func TestIntegration_PgStorageIsRejected(t *testing.T) {
	clearBrainEnv(t)
	dir := setupBrainDir(t)
	writeBrainYML(t, dir, "storage: pg\n")

	code, out := run(t, dir, "--json", "list")
	if code == 0 {
		t.Fatalf("list succeeded with storage: pg, out=%s", out)
	}
	if !strings.Contains(out, "not available yet") {
		t.Errorf("output %q does not explain that pg is unavailable", out)
	}
	if !strings.Contains(out, config.FileName) {
		t.Errorf("output %q does not name the file to edit", out)
	}
}
