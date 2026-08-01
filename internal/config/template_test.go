package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The scaffolded file must round-trip: writing it and loading it back has to
// produce exactly the defaults, with every key still attributed to the
// defaults rather than to the file. If a key were left uncommented, brAIn
// would silently pin today's value into every new project.
func TestTemplate_roundTripsToDefaults(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(Template()), 0o644); err != nil {
		t.Fatalf("writing template: %v", err)
	}

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("generated template does not load: %v", err)
	}

	want := Default()
	if got.Storage != want.Storage || got.Trust != want.Trust ||
		got.Facts != want.Facts || got.Lessons != want.Lessons {
		t.Errorf("template changed a value: got %+v, want %+v", got, want)
	}
	for _, k := range Keys() {
		if src := got.Source(k); src != SourceDefault {
			t.Errorf("Source(%q) = %q, want %q — key %q is not commented out",
				k, src, SourceDefault, k)
		}
	}
}

// Every key and every default value must appear in the file, or the template
// is documenting a subset of what brAIn actually reads.
func TestTemplate_mentionsEveryKeyAndDefault(t *testing.T) {
	tmpl := Template()
	d := Default()

	for _, k := range Keys() {
		// The file uses YAML nesting, so match the leaf rather than the
		// dotted path.
		leaf := k
		if i := strings.LastIndex(k, "."); i >= 0 {
			leaf = k[i+1:]
		}
		if !strings.Contains(tmpl, leaf) {
			t.Errorf("template does not mention key %q", k)
		}
		if name := EnvVar(k); name != "" && !strings.Contains(tmpl, name) {
			t.Errorf("template does not mention env var %q", name)
		}
	}

	// Values are interpolated from Default(), never typed out, so these
	// assertions catch a template that drifted from the real numbers.
	for _, want := range []string{
		string(d.Storage),
		strconv.Itoa(d.Trust.PromoteToNotify),
		strconv.Itoa(d.Trust.PromoteToAutoShip),
		strconv.Itoa(d.Trust.PromoteToFullAuto),
		strconv.Itoa(d.Facts.StaleAfterDays),
		strconv.Itoa(d.Lessons.RetireAfterStreak),
	} {
		if !strings.Contains(tmpl, want) {
			t.Errorf("template does not show default %q", want)
		}
	}
}

// Uncommenting a block must produce a working config, not a syntax error —
// the template is only useful if its examples are valid YAML.
func TestTemplate_uncommentedBlockLoads(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()

	// Uncomment the trust block exactly as a user would.
	var out []string
	for _, line := range strings.Split(Template(), "\n") {
		trimmed := strings.TrimPrefix(line, "# ")
		if strings.HasPrefix(trimmed, "trust:") ||
			strings.HasPrefix(strings.TrimPrefix(line, "#   "), "promote_to_") {
			out = append(out, strings.TrimPrefix(strings.TrimPrefix(line, "# "), "#"))
			continue
		}
		out = append(out, line)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(strings.Join(out, "\n")), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("uncommented trust block does not load: %v", err)
	}
	if got.Trust != Default().Trust {
		t.Errorf("Trust = %+v, want the defaults the template shows", got.Trust)
	}
	if src := got.Source(KeyPromoteNotify); src != SourceFile {
		t.Errorf("Source = %q, want %q now that the key is active", src, SourceFile)
	}
}
