# brAIn

**Persistent, layered memory for AI-assisted projects.**

brAIn gives your AI tools memory that compounds over time — codebase facts, hard-won lessons, settled decisions, trust calibration, and your explicit corrections. Five memory layers plus a trust ladder. Stored as plain Markdown files in a `.brain/` folder, git-backed, human-readable.

Not a vector database. Not a knowledge graph. Not an AI memory product. The lightweight memory substrate your coding tools actually need.

## Status

Pre-alpha. The memory engine, CLI, MCP server, trust ladder, effectiveness tracking, and configuration are implemented. The PostgreSQL adapter and editor integrations are coming in future pitches.

## Install

macOS and Linux (amd64 or arm64):

```bash
curl -fsSL https://raw.githubusercontent.com/luuuc/brain/main/install.sh | sh
```

The script detects your platform, downloads the matching binary from [GitHub Releases](https://github.com/luuuc/brain/releases), verifies its SHA256 checksum, and installs to `/usr/local/bin/brain` (or `~/.local/bin/brain` if `/usr/local/bin` is not writable).

Or with Go:

```bash
go install github.com/luuuc/brain/cmd/brain@latest
```

## Quick Start

```bash
cd your-project
brain init                    # scaffold .brain/ and a commented brain.yml
brain remember "The users table has 12M rows" --domain database --layer fact
brain recall --domain database
brain config                  # what's in effect, and where each value came from
```

Commit `.brain/` to git so your memory travels with the project.

## Configuration

Everything works with no configuration. To change a default, uncomment the
relevant line in `.brain/brain.yml` — `brain init` generates it with every
setting documented at its default value:

```yaml
trust:
  promote_to_notify: 10       # clean outcomes to leave "ask"
facts:
  stale_after_days: 30
lessons:
  retire_after_streak: 20
```

Values resolve as built-in defaults, then `brain.yml`, then `BRAIN_*`
environment variables. `brain config` shows which layer won for each setting.

## How It Works

brAIn stores memories as Markdown files with YAML frontmatter in a `.brain/` folder:

```
.brain/
  facts/
    users-table-12m-rows.md
  lessons/
    payments-race-conditions.md
  decisions/
    camelcase-api-responses.md
  effectiveness/
    persona-kent-beck.md
  corrections/
    2026-04-01-allow-nullable-email.md
  trust/
    trust.yml
```

### Five Memory Layers

| Layer | What | Lifetime |
|---|---|---|
| **Facts** | Codebase truths ("Users table has 12M rows") | Stale after 30 days |
| **Lessons** | Patterns from repeated events ("Migrations need a maintenance window") | Self-retire after 20 clean outcomes |
| **Decisions** | Settled choices ("camelCase for API responses") | Until explicitly revised |
| **Effectiveness** | Persona signal tracking (which reviews helped) | Rolling 90-day window |
| **Corrections** | Owner overrides ("stop flagging nullable email") | Permanent |

### Trust Ladder

Per-domain trust that governs AI autonomy:

| Level | What happens | How it's earned |
|---|---|---|
| `ask` | Human must approve | Default |
| `notify` | Ships, human notified | 10 clean outcomes |
| `auto_ship` | Ships silently | 30 more at `notify` |
| `full_auto` | Full autonomy | 100 more at `auto_ship` |

Trust promotes gradually and demotes immediately on failure.

## What brAIn Is Not

- **Not a vector database.** Markdown by default. pgvector is an optional upgrade.
- **Not a knowledge graph.** Flat files, no edges, no ontology.
- **Not a permanent record.** Facts go stale, lessons retire. Designed to forget what no longer matters.

## Development

```bash
make build    # build the binary
make test     # run tests
make lint     # run linters
make ci       # all of the above
```

## License

MIT
