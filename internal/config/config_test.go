package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// writeConfig creates a .brain/ dir containing brain.yml with the given body
// and returns the dir. A body of "" means no file is written at all.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if body != "" {
		if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
			t.Fatalf("writing config: %v", err)
		}
	}
	return dir
}

func TestDefault(t *testing.T) {
	c := Default()
	if c.Storage != StorageMarkdown {
		t.Errorf("Storage = %q, want %q", c.Storage, StorageMarkdown)
	}
	if c.Trust.PromoteToNotify != 10 || c.Trust.PromoteToAutoShip != 30 || c.Trust.PromoteToFullAuto != 100 {
		t.Errorf("Trust = %+v, want 10/30/100", c.Trust)
	}
	if c.Facts.StaleAfterDays != 30 {
		t.Errorf("StaleAfterDays = %d, want 30", c.Facts.StaleAfterDays)
	}
	if c.Lessons.RetireAfterStreak != 20 {
		t.Errorf("RetireAfterStreak = %d, want 20", c.Lessons.RetireAfterStreak)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("Default() does not validate: %v", err)
	}
}

func TestLoad_absentFileYieldsDefaults(t *testing.T) {
	got, err := Load(writeConfig(t, ""))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Default()
	if got.Trust != want.Trust || got.Facts != want.Facts || got.Lessons != want.Lessons || got.Storage != want.Storage {
		t.Errorf("Load with no file = %+v, want defaults %+v", got, want)
	}
	for _, k := range Keys() {
		if src := got.Source(k); src != SourceDefault {
			t.Errorf("Source(%q) = %q, want %q", k, src, SourceDefault)
		}
	}
}

func TestLoad_emptyAndCommentOnlyFile(t *testing.T) {
	for name, body := range map[string]string{
		"empty":        "\n",
		"comments":     "# nothing here\n# just comments\n",
		"empty_doc":    "---\n",
		"empty_object": "{}\n",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := Load(writeConfig(t, body))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got.Trust != Default().Trust {
				t.Errorf("Trust = %+v, want defaults", got.Trust)
			}
			if src := got.Source(KeyPromoteNotify); src != SourceDefault {
				t.Errorf("Source = %q, want %q", src, SourceDefault)
			}
		})
	}
}

func TestLoad_partialFileOverridesOnlyPresentKeys(t *testing.T) {
	got, err := Load(writeConfig(t, `
trust:
  promote_to_notify: 5
facts:
  stale_after_days: 90
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.Trust.PromoteToNotify != 5 {
		t.Errorf("PromoteToNotify = %d, want 5", got.Trust.PromoteToNotify)
	}
	if got.Facts.StaleAfterDays != 90 {
		t.Errorf("StaleAfterDays = %d, want 90", got.Facts.StaleAfterDays)
	}
	// Untouched keys keep their defaults.
	if got.Trust.PromoteToAutoShip != 30 || got.Trust.PromoteToFullAuto != 100 {
		t.Errorf("unset trust keys drifted: %+v", got.Trust)
	}
	if got.Lessons.RetireAfterStreak != 20 {
		t.Errorf("RetireAfterStreak = %d, want default 20", got.Lessons.RetireAfterStreak)
	}

	if src := got.Source(KeyPromoteNotify); src != SourceFile {
		t.Errorf("Source(%q) = %q, want %q", KeyPromoteNotify, src, SourceFile)
	}
	if src := got.Source(KeyStaleAfterDays); src != SourceFile {
		t.Errorf("Source(%q) = %q, want %q", KeyStaleAfterDays, src, SourceFile)
	}
	if src := got.Source(KeyPromoteAutoShip); src != SourceDefault {
		t.Errorf("Source(%q) = %q, want %q", KeyPromoteAutoShip, src, SourceDefault)
	}
}

func TestLoad_allKeys(t *testing.T) {
	got, err := Load(writeConfig(t, `
storage: markdown
database:
  url: postgres://brain:secret@localhost:5432/brain_dev
embeddings:
  provider: openai
  model: text-embedding-3-small
trust:
  promote_to_notify: 1
  promote_to_auto_ship: 2
  promote_to_full_auto: 3
facts:
  stale_after_days: 7
lessons:
  retire_after_streak: 4
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.Database.URL == "" || got.Embeddings.Provider != EmbeddingOpenAI ||
		got.Embeddings.Model != "text-embedding-3-small" {
		t.Errorf("database/embeddings not parsed: %+v %+v", got.Database, got.Embeddings)
	}
	if got.Trust != (Trust{1, 2, 3}) {
		t.Errorf("Trust = %+v, want {1 2 3}", got.Trust)
	}
	if got.Facts.StaleAfterDays != 7 || got.Lessons.RetireAfterStreak != 4 {
		t.Errorf("facts/lessons = %+v %+v", got.Facts, got.Lessons)
	}

	// Every key was present, so every key reports the file as its source.
	for _, k := range Keys() {
		if src := got.Source(k); src != SourceFile {
			t.Errorf("Source(%q) = %q, want %q", k, src, SourceFile)
		}
	}
}

func TestLoad_unknownKeyIsRejected(t *testing.T) {
	tests := map[string]struct{ body, wantKey string }{
		"top_level": {"promote_to_notify: 5\n", "promote_to_notify"},
		"nested":    {"trust:\n  promote_to_notifiy: 5\n", "promote_to_notifiy"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tc.body))
			if err == nil {
				t.Fatal("Load succeeded, want error naming the unknown key")
			}
			msg := err.Error()
			if !strings.Contains(msg, tc.wantKey) {
				t.Errorf("error %q does not name the bad key %q", msg, tc.wantKey)
			}
			if !strings.Contains(msg, "not a known config key") {
				t.Errorf("error %q lacks the explanatory phrase", msg)
			}
			// The user should never see brAIn's internal Go type names.
			if strings.Contains(msg, "fileConfig") || strings.Contains(msg, "config.file") {
				t.Errorf("error %q leaks an internal type name", msg)
			}
		})
	}
}

func TestLoad_malformedYAML(t *testing.T) {
	_, err := Load(writeConfig(t, "trust:\n  promote_to_notify: [1, 2\n"))
	if err == nil {
		t.Fatal("Load succeeded on malformed YAML, want error")
	}
	if !strings.Contains(err.Error(), FileName) {
		t.Errorf("error %q does not name the config file", err)
	}
}

// A YAML decoder reads only the first document. Accepting the file would
// mean silently discarding every key after the separator.
func TestLoad_multiDocumentIsRejected(t *testing.T) {
	tests := map[string]string{
		"second_doc_valid":   "storage: markdown\n---\ntrust:\n  promote_to_notify: 1\n",
		"second_doc_invalid": "storage: markdown\n---\nbogus_key: 1\n",
		"second_doc_empty":   "storage: markdown\n---\n",
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Load(writeConfig(t, body))
			if err == nil {
				t.Fatal("Load accepted a multi-document file, want error")
			}
			if !strings.Contains(err.Error(), "more than one YAML document") {
				t.Errorf("error %q does not explain the multi-document problem", err)
			}
		})
	}
}

// A leading separator is a single document, not two — it must still load.
func TestLoad_leadingSeparatorIsSingleDocument(t *testing.T) {
	got, err := Load(writeConfig(t, "---\ntrust:\n  promote_to_notify: 5\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Trust.PromoteToNotify != 5 {
		t.Errorf("PromoteToNotify = %d, want 5", got.Trust.PromoteToNotify)
	}
}

func TestLoad_invalidValues(t *testing.T) {
	tests := map[string]struct{ body, wantSubstr string }{
		"unknown_storage":  {"storage: sqlite\n", KeyStorage},
		"unknown_provider": {"embeddings:\n  provider: cohere\n", KeyEmbeddingProvider},
		"zero_threshold":   {"trust:\n  promote_to_notify: 0\n", KeyPromoteNotify},
		"negative_stale":   {"facts:\n  stale_after_days: -1\n", KeyStaleAfterDays},
		"zero_streak":      {"lessons:\n  retire_after_streak: 0\n", KeyRetireAfterStreak},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tc.body))
			if err == nil {
				t.Fatal("Load succeeded, want validation error")
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error %q does not name %q", err, tc.wantSubstr)
			}
		})
	}
}

// An explicitly-set zero must be rejected rather than silently treated as
// "absent" — that distinction is the whole reason fileConfig uses pointers.
func TestLoad_explicitZeroIsNotTreatedAsAbsent(t *testing.T) {
	_, err := Load(writeConfig(t, "trust:\n  promote_to_notify: 0\n"))
	if err == nil {
		t.Fatal("explicit 0 was accepted, want validation error")
	}
}

func TestLoad_unreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions")
	}
	dir := writeConfig(t, "trust:\n  promote_to_notify: 5\n")
	if err := os.Chmod(filepath.Join(dir, FileName), 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if _, err := Load(dir); err == nil {
		t.Error("Load succeeded on unreadable file, want error")
	}
}

// clearEnv unsets every BRAIN_* variable this package reads, so a developer's
// real environment can't leak into these tests.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range Keys() {
		name := EnvVar(k)
		if name == "" {
			continue
		}
		// t.Setenv registers restoration of the original value; Unsetenv then
		// removes it for real. Isolating with an empty value instead would
		// lean on empty-means-unset, the very behaviour these tests verify.
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unsetting %s: %v", name, err)
		}
	}
}

func TestLoad_envOverridesFile(t *testing.T) {
	clearEnv(t)
	t.Setenv("BRAIN_EMBEDDING_MODEL", "from-env")

	got, err := Load(writeConfig(t, "embeddings:\n  provider: openai\n  model: from-file\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Embeddings.Model != "from-env" {
		t.Errorf("Model = %q, want the env value", got.Embeddings.Model)
	}
	if src := got.Source(KeyEmbeddingModel); src != SourceEnv {
		t.Errorf("Source(model) = %q, want %q", src, SourceEnv)
	}
	// A key the environment did not touch keeps the file's value.
	if got.Embeddings.Provider != EmbeddingOpenAI {
		t.Errorf("Provider = %q, want the file value", got.Embeddings.Provider)
	}
	if src := got.Source(KeyEmbeddingProvider); src != SourceFile {
		t.Errorf("Source(provider) = %q, want %q", src, SourceFile)
	}
}

// The environment must apply even when there is no brain.yml at all.
func TestLoad_envAppliesWithoutFile(t *testing.T) {
	clearEnv(t)
	t.Setenv("BRAIN_DATABASE_URL", "postgres://localhost/brain")

	got, err := Load(writeConfig(t, ""))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Database.URL != "postgres://localhost/brain" {
		t.Errorf("URL = %q, want the env value", got.Database.URL)
	}
	if src := got.Source(KeyDatabaseURL); src != SourceEnv {
		t.Errorf("Source = %q, want %q", src, SourceEnv)
	}
}

// The full chain: default beaten by file, file beaten by env, each visible
// through Source.
func TestLoad_precedenceChain(t *testing.T) {
	clearEnv(t)
	t.Setenv("BRAIN_EMBEDDING_MODEL", "env-model")

	got, err := Load(writeConfig(t, `
embeddings:
  provider: ollama
  model: file-model
trust:
  promote_to_notify: 5
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	checks := []struct {
		key      string
		wantSrc  Source
		got, exp any
	}{
		{KeyEmbeddingModel, SourceEnv, got.Embeddings.Model, "env-model"},
		{KeyEmbeddingProvider, SourceFile, got.Embeddings.Provider, EmbeddingOllama},
		{KeyPromoteNotify, SourceFile, got.Trust.PromoteToNotify, 5},
		{KeyPromoteFullAuto, SourceDefault, got.Trust.PromoteToFullAuto, 100},
		{KeyStorage, SourceDefault, got.Storage, StorageMarkdown},
	}
	for _, c := range checks {
		if c.got != c.exp {
			t.Errorf("%s = %v, want %v", c.key, c.got, c.exp)
		}
		if src := got.Source(c.key); src != c.wantSrc {
			t.Errorf("Source(%s) = %q, want %q", c.key, src, c.wantSrc)
		}
	}
}

// An env var that is set but empty means "not configured" — it must not blank
// out a value the file legitimately set.
func TestLoad_emptyEnvIsUnset(t *testing.T) {
	clearEnv(t)
	for name, val := range map[string]string{"empty": "", "whitespace": "   "} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("BRAIN_EMBEDDING_MODEL", val)
			got, err := Load(writeConfig(t, "embeddings:\n  model: from-file\n"))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got.Embeddings.Model != "from-file" {
				t.Errorf("Model = %q, want the file value preserved", got.Embeddings.Model)
			}
			if src := got.Source(KeyEmbeddingModel); src != SourceFile {
				t.Errorf("Source = %q, want %q", src, SourceFile)
			}
		})
	}
}

// Every environment-tunable key must actually be wired into applyEnv.
func TestLoad_everyEnvVarIsWired(t *testing.T) {
	clearEnv(t)
	values := map[string]string{
		"BRAIN_STORAGE":            "markdown",
		"BRAIN_DATABASE_URL":       "postgres://localhost/x",
		"BRAIN_EMBEDDING_PROVIDER": "ollama",
		"BRAIN_EMBEDDING_MODEL":    "nomic-embed-text",
	}
	for name, val := range values {
		t.Setenv(name, val)
	}

	got, err := Load(writeConfig(t, ""))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Storage != StorageMarkdown || got.Database.URL != values["BRAIN_DATABASE_URL"] ||
		got.Embeddings.Provider != EmbeddingOllama || got.Embeddings.Model != values["BRAIN_EMBEDDING_MODEL"] {
		t.Errorf("not every env var was applied: %+v", got)
	}
	for key := range bindings {
		if EnvVar(key) == "" {
			continue
		}
		if src := got.Source(key); src != SourceEnv {
			t.Errorf("Source(%q) = %q, want %q", key, src, SourceEnv)
		}
	}
}

func TestValidationError_Error(t *testing.T) {
	err := &ValidationError{Key: KeyStorage, Msg: "unknown value"}
	if got, want := err.Error(), "storage: unknown value"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestLoad_envValuesAreValidated(t *testing.T) {
	clearEnv(t)
	t.Setenv("BRAIN_STORAGE", "sqlite")

	_, err := Load(writeConfig(t, ""))
	if err == nil {
		t.Fatal("Load accepted an invalid env value, want error")
	}
	// The message must point at the variable, not at a file the user never
	// wrote the value into.
	if !strings.Contains(err.Error(), "$BRAIN_STORAGE") {
		t.Errorf("error %q does not name the offending env var", err)
	}
}

// A bad value in the file must point at the file, not the environment.
func TestLoad_fileValidationErrorNamesFile(t *testing.T) {
	clearEnv(t)
	dir := writeConfig(t, "trust:\n  promote_to_notify: 0\n")
	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load succeeded, want validation error")
	}
	if !strings.Contains(err.Error(), FileName) {
		t.Errorf("error %q does not point at the config file", err)
	}
	if strings.Contains(err.Error(), "$BRAIN") {
		t.Errorf("error %q blames the environment for a file value", err)
	}
}

// Thresholds are deliberately file-only. If that ever changes it should be a
// decision, not an accident.
func TestEnvVar_thresholdsAreNotEnvTunable(t *testing.T) {
	fileOnly := []string{KeyPromoteNotify, KeyPromoteAutoShip, KeyPromoteFullAuto,
		KeyStaleAfterDays, KeyRetireAfterStreak}
	for _, k := range fileOnly {
		if name := EnvVar(k); name != "" {
			t.Errorf("EnvVar(%q) = %q, want no env override", k, name)
		}
	}
	for _, k := range []string{KeyStorage, KeyDatabaseURL, KeyEmbeddingProvider, KeyEmbeddingModel} {
		if EnvVar(k) == "" {
			t.Errorf("EnvVar(%q) is empty, want an env override", k)
		}
	}
}

func TestStaleAfter(t *testing.T) {
	created := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	f := Default().Facts
	want := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	if got := f.StaleAfter(created); !got.Equal(want) {
		t.Errorf("StaleAfter = %v, want %v", got, want)
	}

	f.StaleAfterDays = 1
	if got := f.StaleAfter(created); !got.Equal(created.AddDate(0, 0, 1)) {
		t.Errorf("StaleAfter with 1 day = %v", got)
	}
}

func TestSource_unknownKeyIsDefault(t *testing.T) {
	c := Default()
	if src := c.Source("nope.not.a.key"); src != SourceDefault {
		t.Errorf("Source(unknown) = %q, want %q", src, SourceDefault)
	}
}

// keys and bindings must cover exactly the same set. A key in keys with no
// binding makes brain config print <nil>; a binding missing from keys is
// invisible in both the display and the env overlay.
func TestKeysAndBindingsAgree(t *testing.T) {
	if len(keys) != len(bindings) {
		t.Errorf("keys has %d entries, bindings has %d", len(keys), len(bindings))
	}
	for _, k := range keys {
		b, ok := bindings[k]
		if !ok {
			t.Errorf("key %q has no binding", k)
			continue
		}
		if b.get == nil {
			t.Errorf("binding %q has no getter", k)
		}
		// An env-tunable key needs both halves or it will report a source
		// for a value it never applied.
		if (b.env == "") != (b.set == nil) {
			t.Errorf("binding %q has env=%q but set=%v — both or neither", k, b.env, b.set != nil)
		}
	}
	for k := range bindings {
		if !slices.Contains(keys, k) {
			t.Errorf("binding %q is missing from keys, so nothing displays it", k)
		}
	}
}

// Every key must resolve to a usable value, and unknown keys must not panic.
func TestValue_coversEveryKey(t *testing.T) {
	c := Default()
	for _, k := range Keys() {
		if got := c.Value(k); got == nil {
			t.Errorf("Value(%q) = nil", k)
		}
	}
	if got := c.Value("nope.not.a.key"); got != nil {
		t.Errorf("Value(unknown) = %v, want nil", got)
	}
}

// pg parses and is a known value, but nothing implements it. Accepting it
// would silently write Markdown while the user believed they were on
// Postgres — the config must refuse rather than quietly disagree.
func TestLoad_pgIsRejectedUntilImplemented(t *testing.T) {
	clearEnv(t)
	_, err := Load(writeConfig(t, "storage: pg\n"))
	if err == nil {
		t.Fatal("Load accepted storage: pg, want a not-available error")
	}
	msg := err.Error()
	for _, want := range []string{KeyStorage, "not available yet", string(StorageMarkdown)} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}
	// pg stays a *known* value — the error must not claim it is a typo.
	if strings.Contains(msg, "unknown value") {
		t.Errorf("error %q reports pg as unknown; it is known but unavailable", msg)
	}
}

// The same rejection must apply when pg arrives from the environment.
func TestLoad_pgViaEnvIsRejected(t *testing.T) {
	clearEnv(t)
	t.Setenv("BRAIN_STORAGE", string(StoragePG))

	_, err := Load(writeConfig(t, ""))
	if err == nil {
		t.Fatal("Load accepted BRAIN_STORAGE=pg, want an error")
	}
	if !strings.Contains(err.Error(), "$BRAIN_STORAGE") {
		t.Errorf("error %q does not name the env var that set it", err)
	}
}

// Storage.Valid stays true for pg: known and available are different
// questions, and Validate is what answers the second.
func TestStorageValid_pgIsKnown(t *testing.T) {
	if !StoragePG.Valid() {
		t.Error("StoragePG.Valid() = false; pg is a known value, just unavailable")
	}
	if StorageMarkdown.Valid() != true || Storage("sqlite").Valid() != false {
		t.Error("Storage.Valid is wrong for markdown or an unknown value")
	}
}
