# brAIn

**Persistent, layered memory for AI-assisted projects.**

brAIn gives your AI tools memory that compounds over time — codebase facts, hard-won lessons, settled decisions, trust calibration, and your explicit corrections. Five memory layers plus a trust ladder. Stored as plain Markdown files in a `.brain/` folder, git-backed, human-readable.

Not a vector database. Not a knowledge graph. Not an AI memory product. The lightweight memory substrate your coding tools actually need.

## Status

Pre-alpha. The storage interface and markdown adapter are implemented. CLI and MCP server are coming in future pitches.

## Install

From source:

```bash
go install github.com/luuuc/brain@latest
```

Or download a binary from the [releases page](https://github.com/luuuc/brain/releases).

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
