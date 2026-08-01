package config

import (
	"fmt"
	"strings"
)

// Template renders a fully commented brain.yml showing every key at its
// built-in default.
//
// Every key is commented out. An active key would pin today's default into
// the file forever — a later brAIn release that changed a default would have
// no effect, and `brain config` would report every value as coming from the
// file rather than from the defaults, making its source column useless. The
// file is documentation until you uncomment something.
//
// Values are interpolated from Default() rather than typed out, so the
// scaffolded file cannot drift from the numbers the engines actually use.
func Template() string {
	d := Default()
	var b strings.Builder

	b.WriteString(`# brAIn configuration
#
# Every setting below is shown at its built-in default and commented out.
# Uncomment a line to override it. Deleting this file is safe — brAIn runs
# on the defaults with no config at all.
#
# Precedence, lowest to highest: built-in defaults, this file, environment
# variables. Run "brain config" to see where each value is coming from.

# Storage backend. Only "markdown" is available today; "pg" is planned.
# Override with $` + EnvVar(KeyStorage) + `.
`)
	fmt.Fprintf(&b, "# %s: %s\n\n", KeyStorage, d.Storage)

	b.WriteString(`# PostgreSQL connection, used only when storage is "pg".
# Override with $` + EnvVar(KeyDatabaseURL) + `.
# database:
#   url: postgres://brain:secret@localhost:5432/brain_dev

# Embedding model for semantic recall, used only when storage is "pg".
# Providers: ` + string(EmbeddingOpenAI) + `, ` + string(EmbeddingAnthropic) + `, ` + string(EmbeddingOllama) + `.
# Override with $` + EnvVar(KeyEmbeddingProvider) + ` and $` + EnvVar(KeyEmbeddingModel) + `.
# embeddings:
#   provider: ` + string(EmbeddingOpenAI) + `
#   model: text-embedding-3-small

# Trust ladder: clean outcomes needed to climb each rung. The counter resets
# to zero at every promotion, so these are per-level, not cumulative.
#
# These gate the edges BETWEEN levels, not the levels themselves. Raising a
# threshold does not demote a domain that is already past it — a domain at
# auto_ship stays there no matter what you set promote_to_notify to. Use
# "brain trust override" to move a domain down deliberately.
`)
	b.WriteString("# trust:\n")
	fmt.Fprintf(&b, "#   promote_to_notify: %d\n", d.Trust.PromoteToNotify)
	fmt.Fprintf(&b, "#   promote_to_auto_ship: %d\n", d.Trust.PromoteToAutoShip)
	fmt.Fprintf(&b, "#   promote_to_full_auto: %d\n", d.Trust.PromoteToFullAuto)
	fmt.Fprintf(&b, "#   # Total to reach full_auto: %d\n\n",
		d.Trust.PromoteToNotify+d.Trust.PromoteToAutoShip+d.Trust.PromoteToFullAuto)

	b.WriteString(`# How long a fact stays fresh. Stamped onto each fact when it is written,
# so changing this affects new facts only — facts already on disk keep the
# expiry they were created with.
`)
	b.WriteString("# facts:\n")
	fmt.Fprintf(&b, "#   stale_after_days: %d\n\n", d.Facts.StaleAfterDays)

	b.WriteString(`# Clean-outcome streak at which a lesson retires. A lesson can override
# this with its own retire_after frontmatter field.
#
# Retirement is permanent. Raising this does nothing for lessons the old,
# lower value already retired. Lowering it can retire several lessons at once
# on the next clean outcome.
`)
	b.WriteString("# lessons:\n")
	fmt.Fprintf(&b, "#   retire_after_streak: %d\n", d.Lessons.RetireAfterStreak)

	return b.String()
}
