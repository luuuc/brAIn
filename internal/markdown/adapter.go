package markdown

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/luuuc/brain/internal/memory"
	"github.com/luuuc/brain/internal/store"
)

// Adapter implements store.Store against a .brain/ directory on disk.
type Adapter struct {
	root string // absolute path to the .brain/ directory
}

// New creates a markdown adapter rooted at the given directory.
// The directory does not need to exist yet — it will be created on first write.
func New(root string) *Adapter {
	return &Adapter{root: root}
}

func (a *Adapter) Write(_ context.Context, m memory.Memory) (string, error) {
	var relPath string

	if m.Path != "" {
		// Update: overwrite existing file
		relPath = m.Path
	} else {
		// Create: generate path from layer + slug, avoiding collisions
		dir := layerDir(m.Layer)
		slug := slugify(m)
		relPath = filepath.Join(dir, slug+".md")

		absCandidate := filepath.Join(a.root, relPath)
		for i := 2; fileExists(absCandidate); i++ {
			relPath = filepath.Join(dir, fmt.Sprintf("%s-%d.md", slug, i))
			absCandidate = filepath.Join(a.root, relPath)
		}
	}

	absPath := filepath.Join(a.root, relPath)

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}

	data, err := marshal(m)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}

	// Atomic write: temp file + rename
	tmp := absPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, absPath); err != nil {
		_ = os.Remove(tmp) // best-effort cleanup
		return "", fmt.Errorf("rename: %w", err)
	}

	return relPath, nil
}

func (a *Adapter) Read(_ context.Context, path string) (memory.Memory, error) {
	absPath, err := a.safePath(path)
	if err != nil {
		return memory.Memory{}, err
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return memory.Memory{}, store.ErrNotFound
		}
		return memory.Memory{}, fmt.Errorf("read: %w", err)
	}

	m, err := unmarshal(data)
	if err != nil {
		return memory.Memory{}, fmt.Errorf("unmarshal %s: %w", path, err)
	}
	m.Path = path

	return m, nil
}

func (a *Adapter) List(_ context.Context, f store.Filter) ([]memory.Memory, error) {
	result := make([]memory.Memory, 0)

	// Determine which layer directories to scan
	dirs := a.layerDirs(f.Layer)

	for _, dir := range dirs {
		absDir := filepath.Join(a.root, dir)

		entries, err := os.ReadDir(absDir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return result, fmt.Errorf("readdir %s: %w", dir, err)
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}

			relPath := filepath.Join(dir, entry.Name())
			absPath := filepath.Join(absDir, entry.Name())

			data, err := os.ReadFile(absPath)
			if err != nil {
				return result, fmt.Errorf("read %s: %w", relPath, err)
			}

			m, err := unmarshal(data)
			if err != nil {
				return result, fmt.Errorf("unmarshal %s: %w", relPath, err)
			}
			m.Path = relPath

			if matchesFilter(m, f) {
				result = append(result, m)
			}
		}
	}

	return result, nil
}

// AppendBody appends content to an existing file's body using O_APPEND,
// bypassing the full read-modify-write cycle. The file must already
// exist; callers use Write for creation. Intended for append-only logs
// (effectiveness outcome entries) where rewriting the whole body on
// every record would be quadratic over the file's lifetime.
//
// Atomicity note: a partial write on a crash can leave a truncated
// trailing line. Readers tolerate that — unparseable lines are kept
// verbatim by the engine's loadOutcomes and skipped by entries().
func (a *Adapter) AppendBody(_ context.Context, path, content string) error {
	absPath, err := a.safePath(path)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(absPath, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return store.ErrNotFound
		}
		return fmt.Errorf("append: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(content); err != nil {
		return fmt.Errorf("append write: %w", err)
	}
	return nil
}

func (a *Adapter) Delete(_ context.Context, path string) error {
	absPath, err := a.safePath(path)
	if err != nil {
		return err
	}

	err = os.Remove(absPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return store.ErrNotFound
		}
		return fmt.Errorf("delete: %w", err)
	}

	return nil
}

// safePath validates that a relative path does not escape the root directory.
func (a *Adapter) safePath(rel string) (string, error) {
	abs := filepath.Join(a.root, rel)
	abs = filepath.Clean(abs)
	if !strings.HasPrefix(abs, filepath.Clean(a.root)+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes root directory", rel)
	}
	return abs, nil
}

// layerDirs returns the subdirectories to scan. If a layer filter is set,
// only that layer's directory is returned; otherwise all five are returned.
func (a *Adapter) layerDirs(layer *memory.Layer) []string {
	if layer != nil {
		return []string{layerDir(*layer)}
	}
	return []string{
		layerDir(memory.LayerFact),
		layerDir(memory.LayerLesson),
		layerDir(memory.LayerDecision),
		layerDir(memory.LayerEffectiveness),
		layerDir(memory.LayerCorrection),
	}
}

var layerDirNames = map[memory.Layer]string{
	memory.LayerFact:          "facts",
	memory.LayerLesson:        "lessons",
	memory.LayerDecision:      "decisions",
	memory.LayerEffectiveness: "effectiveness",
	memory.LayerCorrection:    "corrections",
}

func layerDir(l memory.Layer) string {
	if dir, ok := layerDirNames[l]; ok {
		return dir
	}
	return string(l)
}

func matchesFilter(m memory.Memory, f store.Filter) bool {
	if f.Layer != nil && m.Layer != *f.Layer {
		return false
	}
	if f.Domain != nil && m.Domain != *f.Domain {
		return false
	}
	if len(f.Tags) > 0 {
		tagSet := make(map[string]bool, len(m.Tags))
		for _, t := range m.Tags {
			tagSet[t] = true
		}
		for _, required := range f.Tags {
			if !tagSet[required] {
				return false
			}
		}
	}
	return true
}

// marshal renders a Memory as a markdown file with YAML frontmatter.
func marshal(m memory.Memory) ([]byte, error) {
	var buf bytes.Buffer

	fm, err := yaml.Marshal(m)
	if err != nil {
		return nil, err
	}

	buf.WriteString("---\n")
	buf.Write(fm)
	buf.WriteString("---\n")
	if m.Body != "" {
		buf.WriteString(m.Body)
	}

	return buf.Bytes(), nil
}

// unmarshal parses a markdown file with YAML frontmatter into a Memory.
// Splits on the first two "---" lines only.
func unmarshal(data []byte) (memory.Memory, error) {
	content := string(data)

	if !strings.HasPrefix(content, "---\n") {
		return memory.Memory{}, fmt.Errorf("missing opening frontmatter delimiter")
	}

	// Find the closing "---"
	rest := content[4:] // skip opening "---\n"
	idx := strings.Index(rest, "\n---\n")
	if idx < 0 {
		return memory.Memory{}, fmt.Errorf("missing closing frontmatter delimiter")
	}

	frontmatter := rest[:idx]
	body := rest[idx+5:] // skip "\n---\n"

	var m memory.Memory
	if err := yaml.Unmarshal([]byte(frontmatter), &m); err != nil {
		return memory.Memory{}, fmt.Errorf("yaml: %w", err)
	}
	m.Body = body

	return m, nil
}

// slugify generates a kebab-case filename slug from a memory.
// Corrections get a date prefix. Truncated at 60 chars.
func slugify(m memory.Memory) string {
	// Use the first line of the body as the title, stripping markdown heading prefix
	title := firstLine(m.Body)
	title = strings.TrimLeft(title, "# ")

	if title == "" {
		title = m.Domain
	}

	slug := kebab(title)
	if len(slug) > 60 {
		slug = slug[:60]
		// Don't end on a hyphen
		slug = strings.TrimRight(slug, "-")
	}

	if m.Layer == memory.LayerCorrection {
		slug = m.Created.Format(time.DateOnly) + "-" + slug
	}

	return slug
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func kebab(s string) string {
	var buf strings.Builder
	prevDash := false

	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			buf.WriteRune(unicode.ToLower(r))
			prevDash = false
		} else if !prevDash && buf.Len() > 0 {
			buf.WriteByte('-')
			prevDash = true
		}
	}

	result := buf.String()
	return strings.TrimRight(result, "-")
}
