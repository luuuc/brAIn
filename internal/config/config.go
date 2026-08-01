// Package config loads brAIn's optional .brain/brain.yml, layering it over
// built-in defaults. Every tunable brAIn advertises lives here — trust
// promotion thresholds, fact staleness, lesson retirement — so the numbers
// have exactly one home.
//
// A missing brain.yml is not an error. It means "all defaults", which is the
// documented Markdown-adapter experience: brain works the moment you run it.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// FileName is the config file's name within the .brain/ directory.
const FileName = "brain.yml"

// Storage names a storage backend.
type Storage string

const (
	StorageMarkdown Storage = "markdown"
	StoragePG       Storage = "pg"
)

// Valid reports whether s is a known storage backend. Known is not the same
// as available — see Config.Validate.
func (s Storage) Valid() bool {
	switch s {
	case StorageMarkdown, StoragePG:
		return true
	}
	return false
}

// EmbeddingProvider names an embedding backend. Parsed and validated only;
// no provider client exists yet (see pitch 02-02).
type EmbeddingProvider string

const (
	EmbeddingOpenAI    EmbeddingProvider = "openai"
	EmbeddingAnthropic EmbeddingProvider = "anthropic"
	EmbeddingOllama    EmbeddingProvider = "ollama"
)

// Valid reports whether p is a known embedding provider.
func (p EmbeddingProvider) Valid() bool {
	switch p {
	case EmbeddingOpenAI, EmbeddingAnthropic, EmbeddingOllama:
		return true
	}
	return false
}

// Config is the fully resolved configuration: defaults, overlaid with
// brain.yml, overlaid with environment variables.
type Config struct {
	Storage    Storage
	Database   Database
	Embeddings Embeddings
	Trust      Trust
	Facts      Facts
	Lessons    Lessons

	// sources records where each value came from, keyed by the dotted config
	// path (see the Key* constants). Populated by Load; nil for a bare
	// Default(), where Source reports SourceDefault for everything anyway.
	//
	// Written only during Load, before the Config escapes. Config travels by
	// value but this map does not — every copy shares it — so it must stay
	// read-only afterwards.
	sources map[string]Source

	// path is the brain.yml this Config was loaded from, whether or not the
	// file existed. Used to point error messages at something editable.
	path string
}

// Database configures the PostgreSQL adapter. Parsed only — the adapter
// itself is pitch 02-02.
type Database struct {
	URL string
}

// Embeddings configures the embedding model used for semantic recall.
// Parsed only — nothing calls a provider API yet.
type Embeddings struct {
	Provider EmbeddingProvider
	Model    string
}

// Trust holds the clean-outcome counts needed to climb each rung of the
// trust ladder. The counter resets to zero at each promotion, so these are
// per-level, not cumulative.
//
// These thresholds gate the *edges between levels*, not the levels
// themselves. Raising one does not demote a domain that is already past it —
// use `brain trust override` for that.
type Trust struct {
	PromoteToNotify   int
	PromoteToAutoShip int
	PromoteToFullAuto int
}

// Facts configures the fact layer.
type Facts struct {
	// StaleAfterDays is how long a fact stays fresh. Stamped onto a fact's
	// stale_after at write time, never applied retroactively on read.
	StaleAfterDays int
}

// StaleAfter returns the stale_after timestamp for a fact created at the
// given time. Calendar days, not fixed 24-hour periods, so "30 days" means
// the same wall-clock time 30 days later regardless of any DST transition.
func (f Facts) StaleAfter(created time.Time) time.Time {
	return created.AddDate(0, 0, f.StaleAfterDays)
}

// Lessons configures the lesson layer.
type Lessons struct {
	// RetireAfterStreak is the clean-outcome streak at which a lesson
	// retires. Individual lessons may override it via their retire_after
	// frontmatter field.
	RetireAfterStreak int
}

// Source identifies where a resolved config value came from.
type Source string

const (
	SourceDefault Source = "default"
	SourceFile    Source = "file"
	SourceEnv     Source = "env"
)

// Dotted config paths, used for source tracking and error messages.
const (
	KeyStorage           = "storage"
	KeyDatabaseURL       = "database.url"
	KeyEmbeddingProvider = "embeddings.provider"
	KeyEmbeddingModel    = "embeddings.model"
	KeyPromoteNotify     = "trust.promote_to_notify"
	KeyPromoteAutoShip   = "trust.promote_to_auto_ship"
	KeyPromoteFullAuto   = "trust.promote_to_full_auto"
	KeyStaleAfterDays    = "facts.stale_after_days"
	KeyRetireAfterStreak = "lessons.retire_after_streak"
)

// binding is everything the package knows about one config key: how to read
// its value, and — for the keys the environment can override — the variable
// name and how to apply it.
//
// Reader, variable name, and setter live in one entry so a key can never be
// listed as environment-tunable without actually being applied, and can never
// be displayed by brain config from a different field than the one env writes
// to. Both would make brAIn misreport its own configuration.
type binding struct {
	get func(Config) any
	env string                // empty for file-only keys
	set func(*Config, string) // nil for file-only keys
}

// bindings covers every key in keys. The thresholds have no env entry
// deliberately: a threshold set by an invisible variable is a trust ladder
// nobody can audit.
var bindings = map[string]binding{
	KeyStorage: {
		get: func(c Config) any { return string(c.Storage) },
		env: "BRAIN_STORAGE",
		set: func(c *Config, v string) { c.Storage = Storage(v) },
	},
	KeyDatabaseURL: {
		get: func(c Config) any { return c.Database.URL },
		env: "BRAIN_DATABASE_URL",
		set: func(c *Config, v string) { c.Database.URL = v },
	},
	KeyEmbeddingProvider: {
		get: func(c Config) any { return string(c.Embeddings.Provider) },
		env: "BRAIN_EMBEDDING_PROVIDER",
		set: func(c *Config, v string) { c.Embeddings.Provider = EmbeddingProvider(v) },
	},
	KeyEmbeddingModel: {
		get: func(c Config) any { return c.Embeddings.Model },
		env: "BRAIN_EMBEDDING_MODEL",
		set: func(c *Config, v string) { c.Embeddings.Model = v },
	},
	KeyPromoteNotify:     {get: func(c Config) any { return c.Trust.PromoteToNotify }},
	KeyPromoteAutoShip:   {get: func(c Config) any { return c.Trust.PromoteToAutoShip }},
	KeyPromoteFullAuto:   {get: func(c Config) any { return c.Trust.PromoteToFullAuto }},
	KeyStaleAfterDays:    {get: func(c Config) any { return c.Facts.StaleAfterDays }},
	KeyRetireAfterStreak: {get: func(c Config) any { return c.Lessons.RetireAfterStreak }},
}

// EnvVar returns the environment variable that overrides the given config
// key, or "" if the key can only be set in brain.yml.
func EnvVar(key string) string { return bindings[key].env }

// Value returns the resolved value at the given dotted key: a string for
// storage, database and embedding keys, an int for the numeric ones. Unknown
// keys return nil.
func (c Config) Value(key string) any {
	b, ok := bindings[key]
	if !ok {
		return nil
	}
	return b.get(c)
}

// Keys returns every dotted config path in display order.
func Keys() []string { return slices.Clone(keys) }

var keys = []string{
	KeyStorage,
	KeyDatabaseURL,
	KeyEmbeddingProvider,
	KeyEmbeddingModel,
	KeyPromoteNotify,
	KeyPromoteAutoShip,
	KeyPromoteFullAuto,
	KeyStaleAfterDays,
	KeyRetireAfterStreak,
}

// Default returns the built-in configuration. These values are the single
// source of truth for brAIn's advertised defaults; the engines read them
// rather than carrying their own constants.
func Default() Config {
	return Config{
		Storage: StorageMarkdown,
		Trust: Trust{
			PromoteToNotify:   10,
			PromoteToAutoShip: 30,
			PromoteToFullAuto: 100,
		},
		Facts:   Facts{StaleAfterDays: 30},
		Lessons: Lessons{RetireAfterStreak: 20},
	}
}

// Source reports where the value at the given dotted key came from.
// Unknown keys, and every key of a Config built by Default, report
// SourceDefault.
func (c Config) Source(key string) Source {
	if s, ok := c.sources[key]; ok {
		return s
	}
	return SourceDefault
}

// fileConfig mirrors Config with pointer fields so Load can tell "key
// absent" from "key explicitly set to a zero value". Keeping it separate
// from Config is what makes source tracking exact.
type fileConfig struct {
	Storage    *string         `yaml:"storage"`
	Database   *fileDatabase   `yaml:"database"`
	Embeddings *fileEmbeddings `yaml:"embeddings"`
	Trust      *fileTrust      `yaml:"trust"`
	Facts      *fileFacts      `yaml:"facts"`
	Lessons    *fileLessons    `yaml:"lessons"`
}

type fileDatabase struct {
	URL *string `yaml:"url"`
}

type fileEmbeddings struct {
	Provider *string `yaml:"provider"`
	Model    *string `yaml:"model"`
}

type fileTrust struct {
	PromoteToNotify   *int `yaml:"promote_to_notify"`
	PromoteToAutoShip *int `yaml:"promote_to_auto_ship"`
	PromoteToFullAuto *int `yaml:"promote_to_full_auto"`
}

type fileFacts struct {
	StaleAfterDays *int `yaml:"stale_after_days"`
}

type fileLessons struct {
	RetireAfterStreak *int `yaml:"retire_after_streak"`
}

// Load resolves the configuration for the given .brain/ directory:
// defaults, overlaid with brain.yml if present, then validated.
//
// An absent brain.yml yields Default(). An unreadable or malformed one is an
// error — silently falling back to defaults would hide a typo'd threshold,
// which is the failure mode this package exists to prevent.
func Load(brainDir string) (Config, error) {
	cfg := Default()
	cfg.sources = make(map[string]Source, len(keys))
	cfg.path = filepath.Join(brainDir, FileName)

	data, err := os.ReadFile(cfg.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// No file is not an error — it means "all defaults".
	case err != nil:
		return Config{}, fmt.Errorf("config: reading %s: %w", cfg.path, err)
	default:
		fc, err := parseFile(data, cfg.path)
		if err != nil {
			return Config{}, err
		}
		cfg.applyFile(fc)
	}

	cfg.applyEnv()

	if err := cfg.Validate(); err != nil {
		var ve *ValidationError
		if errors.As(err, &ve) {
			return Config{}, fmt.Errorf("config: %s (%s): %s", ve.Key, cfg.origin(ve.Key), ve.Msg)
		}
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}

// parseFile decodes brain.yml, rejecting unknown keys and multi-document
// files.
func parseFile(data []byte, path string) (fileConfig, error) {
	var fc fileConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	// io.EOF means the file is empty or all comments — same as absent.
	if err := dec.Decode(&fc); err != nil && !errors.Is(err, io.EOF) {
		return fileConfig{}, fmt.Errorf("config: parsing %s: %s", path, cleanYAMLError(err))
	}

	// A second document would be silently discarded by the decoder above.
	// Silently discarding configuration is the exact failure this package
	// exists to prevent, so refuse the file instead.
	var extra fileConfig
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return fileConfig{}, fmt.Errorf(
			"config: parsing %s: file contains more than one YAML document; brain.yml must be a single document", path)
	}
	return fc, nil
}

// applyEnv overlays BRAIN_* environment variables onto c, recording their
// source. A variable that is set but empty counts as unset: empty is never a
// meaningful value for any of these keys, and passing an empty variable is
// how compose files and CI runners spell "not configured".
func (c *Config) applyEnv() {
	for _, key := range keys {
		b := bindings[key]
		if b.env == "" {
			continue
		}
		// Trim so a copy-pasted value with stray whitespace still works, and
		// so a whitespace-only variable reads as unset.
		val := strings.TrimSpace(os.Getenv(b.env))
		if val == "" {
			continue
		}
		b.set(c, val)
		c.sources[key] = SourceEnv
	}
}

// origin describes where the value at key came from, in terms a user can act
// on: the variable to unset, or the file to edit.
func (c Config) origin(key string) string {
	switch c.Source(key) {
	case SourceEnv:
		return "from $" + EnvVar(key)
	case SourceFile:
		// Load always sets path before anything can be sourced from a file.
		return "from " + c.path
	default:
		return "built-in default"
	}
}

// applyFile overlays present file values onto c, recording their source.
func (c *Config) applyFile(fc fileConfig) {
	set := func(key string) { c.sources[key] = SourceFile }

	if fc.Storage != nil {
		c.Storage = Storage(*fc.Storage)
		set(KeyStorage)
	}
	if d := fc.Database; d != nil && d.URL != nil {
		c.Database.URL = *d.URL
		set(KeyDatabaseURL)
	}
	if e := fc.Embeddings; e != nil {
		if e.Provider != nil {
			c.Embeddings.Provider = EmbeddingProvider(*e.Provider)
			set(KeyEmbeddingProvider)
		}
		if e.Model != nil {
			c.Embeddings.Model = *e.Model
			set(KeyEmbeddingModel)
		}
	}
	if t := fc.Trust; t != nil {
		if t.PromoteToNotify != nil {
			c.Trust.PromoteToNotify = *t.PromoteToNotify
			set(KeyPromoteNotify)
		}
		if t.PromoteToAutoShip != nil {
			c.Trust.PromoteToAutoShip = *t.PromoteToAutoShip
			set(KeyPromoteAutoShip)
		}
		if t.PromoteToFullAuto != nil {
			c.Trust.PromoteToFullAuto = *t.PromoteToFullAuto
			set(KeyPromoteFullAuto)
		}
	}
	if f := fc.Facts; f != nil && f.StaleAfterDays != nil {
		c.Facts.StaleAfterDays = *f.StaleAfterDays
		set(KeyStaleAfterDays)
	}
	if l := fc.Lessons; l != nil && l.RetireAfterStreak != nil {
		c.Lessons.RetireAfterStreak = *l.RetireAfterStreak
		set(KeyRetireAfterStreak)
	}
}

// ValidationError is a rejected config value. It carries the dotted key so
// callers can report where the value came from.
type ValidationError struct {
	Key string
	Msg string
}

func (e *ValidationError) Error() string { return e.Key + ": " + e.Msg }

// Validate checks the resolved configuration for values brAIn cannot honour.
// Every error it returns is a *ValidationError.
func (c Config) Validate() error {
	if !c.Storage.Valid() {
		return &ValidationError{Key: KeyStorage, Msg: fmt.Sprintf(
			"unknown value %q (valid: %s, %s)", c.Storage, StorageMarkdown, StoragePG)}
	}
	// pg is a known value with no implementation behind it. Rejecting it here
	// beats accepting it and silently writing Markdown, which would look like
	// it worked until someone went looking for their data in Postgres.
	if c.Storage == StoragePG {
		return &ValidationError{Key: KeyStorage, Msg: fmt.Sprintf(
			"%q is not available yet — the PostgreSQL adapter is still in development. Use %q, which is the default",
			StoragePG, StorageMarkdown)}
	}
	if p := c.Embeddings.Provider; p != "" && !p.Valid() {
		return &ValidationError{Key: KeyEmbeddingProvider, Msg: fmt.Sprintf(
			"unknown value %q (valid: %s, %s, %s)", p, EmbeddingOpenAI, EmbeddingAnthropic, EmbeddingOllama)}
	}

	positive := []struct {
		key string
		val int
	}{
		{KeyPromoteNotify, c.Trust.PromoteToNotify},
		{KeyPromoteAutoShip, c.Trust.PromoteToAutoShip},
		{KeyPromoteFullAuto, c.Trust.PromoteToFullAuto},
		{KeyStaleAfterDays, c.Facts.StaleAfterDays},
		{KeyRetireAfterStreak, c.Lessons.RetireAfterStreak},
	}
	for _, p := range positive {
		if p.val < 1 {
			return &ValidationError{Key: p.key, Msg: fmt.Sprintf("must be at least 1, got %d", p.val)}
		}
	}
	return nil
}

// cleanYAMLError rewrites yaml.v3's decode errors so they name the offending
// key without leaking brAIn's internal Go type names at the user.
func cleanYAMLError(err error) string {
	var te *yaml.TypeError
	if !errors.As(err, &te) {
		return err.Error()
	}
	msgs := make([]string, 0, len(te.Errors))
	for _, m := range te.Errors {
		// "line 3: field foo not found in type config.fileTrust"
		if i := strings.Index(m, " not found in type "); i >= 0 {
			m = m[:i] + " is not a known config key"
		}
		msgs = append(msgs, m)
	}
	return strings.Join(msgs, "; ")
}
