package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/luuuc/brain/internal/config"
	"github.com/luuuc/brain/internal/engine"
	"github.com/luuuc/brain/internal/markdown"
	"github.com/luuuc/brain/internal/memory"
	"github.com/luuuc/brain/internal/trust"
)

// sessionStartLimit is how many memories a session opens with. Every session
// pays for these in tokens before anyone types anything, so the number stays
// small deliberately: a hook that injects thirty memories teaches a model to
// skim them. Anything beyond the top five is the model's job to ask for, by
// domain, once it knows what it is touching.
const sessionStartLimit = 5

const sessionStartLongHelp = `Print project memory for an editor's session-start hook.

Writes the project's highest-authority memories to stdout, framed so a model
knows what outranks what. Claude Code adds a SessionStart hook's stdout to
the model's context, so this is what a session knows before anyone types.

It always exits 0 and it never prints a diagnostic. A session-start hook
that exits 2 blocks the session, and the states that would cause it — no
.brain/ directory, an empty one, an unreadable config — are ordinary rather
than exceptional. Having nothing to say is not an error; it prints nothing.

No domain is passed on purpose. A hook cannot know which domain a session is
about, and brAIn does not guess. Recall with no domain ranks across all of
them by layer authority, which is the right shape for "here is what is
non-negotiable in this project".

Output is plain text, not JSON, and deliberately so: the hook payload schema
belongs to the editor, and brAIn should not have to ship a release when
someone else's protocol changes.`

const sessionStartHeader = `# Project memory (brAIn)

Ranked by authority, most authoritative first. Corrections are
non-negotiable owner directives. Decisions are settled — do not relitigate
them unless their revisit condition is met.

`

const sessionStartFooter = `Before working in a specific area, run ` +
	"`brain recall --domain <domain>`" + ` for the memories that apply to it.
`

// claudeSettingsSnippet is the SessionStart block for .claude/settings.json.
//
// The timeout is load-bearing rather than decoration. Nothing inside the
// hook can bound how long a recall takes — "timeout" is not portable to
// macOS, which is half of brAIn's supported platforms — so the only place
// the bound can live is here, in the editor's own configuration. Without it
// a wedged recall stalls the session behind it.
//
// Ten seconds is generous for reading a handful of markdown files, and short
// enough that a person notices the stall rather than assuming the editor
// hung.
//
// The matcher covers the three events that leave a session without memory:
// it starts, it is cleared, or it is compacted. Compaction is the one most
// worth having — that is where a correction gets summarised away and the
// model quietly loses the thing it was told not to do. "resume" and "fork"
// are excluded because both inherit a context that already has the
// injection in it.
const claudeSettingsSnippet = `{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "startup|clear|compact",
        "hooks": [
          {
            "type": "command",
            "command": "brain hooks session-start",
            "timeout": 10
          }
        ]
      }
    ]
  }
}`

const hooksLongHelp = `Print the editor configuration that runs the memory loop.

Everything is written to stdout. brAIn never edits your settings, your
CLAUDE.md, or anything else you own — it prints, you install. Merging into a
settings file you have already customised is your editor's problem to get
right, not a memory tool's.`

// The editors brAIn knows how to set up.
const (
	toolClaudeCode = "claude-code"
	toolCursor     = "cursor"
)

func hooksCmd() *cobra.Command {
	var tool string

	cmd := &cobra.Command{
		Use:   "hooks",
		Short: "Print editor configuration for the memory loop",
		Long:  hooksLongHelp,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch tool {
			case toolClaudeCode:
				fmt.Print(claudeCodeSetup())
			case toolCursor:
				fmt.Print(cursorSetup())
			default:
				return &ExitError{Code: 3, Err: fmt.Errorf(
					"unknown tool %q (valid: %s, %s)", tool, toolClaudeCode, toolCursor)}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&tool, "tool", toolClaudeCode,
		"editor to print setup for (claude-code, cursor)")
	cmd.AddCommand(sessionStartCmd())
	return cmd
}

// cursorFrontmatter is the .mdc header Cursor reads.
//
// alwaysApply is true because memory is not the kind of context that belongs
// to particular files. Scoping it with globs would hide it precisely when
// someone works somewhere nobody predicted, which is when a correction is
// most worth having.
const cursorFrontmatter = `---
description: Project memory (brAIn) — recall before changing code, record what compounds
globs:
alwaysApply: true
---`

// cursorSetup renders the install instructions for Cursor.
func cursorSetup() string {
	var b strings.Builder
	b.WriteString(`# brAIn in Cursor

Save this as .cursor/rules/brain.mdc in your project:

`)
	b.WriteString(cursorFrontmatter)
	b.WriteString("\n\n")
	b.WriteString(memoryRules(cursorIntro))
	return b.String()
}

// claudeCodeSetup renders the install instructions for Claude Code.
func claudeCodeSetup() string {
	var b strings.Builder
	b.WriteString(`# brAIn in Claude Code

1. Add this to .claude/settings.json in your project:

`)
	b.WriteString(claudeSettingsSnippet)
	b.WriteString(`

Put it in the project rather than in ~/.claude/settings.json. A global hook
runs in every project you open, including the ones with no .brain/ — it
costs nothing there, but the project file is also the one that travels with
the repository, so the rest of the team gets the same memory you do.

The matcher covers the three events that leave a session with no memory:
it starts, it is cleared, or it is compacted. Compaction matters most — it
is where a correction gets summarised away and the model quietly loses the
thing it was told not to do. Resumed and forked sessions are left out
because they inherit a context that already has it.

2. Add this to your CLAUDE.md:

`)
	b.WriteString(memoryRules(claudeIntro))
	return b.String()
}

// Where the memories come from differs by editor, and the rules have to say
// so honestly: an instruction to rely on an injection that never happens is
// worse than an instruction to go and fetch it.
const (
	claudeIntro = `This project remembers things in ~.brain/~. A session-start hook loads the
top of it for you. These rules cover the rest.`

	cursorIntro = `This project remembers things in ~.brain/~. Nothing injects it for you with
this setup, so the first thing to do in a session that touches code is
recall.`
)

// memoryRules is the block a user pastes into their editor's rules file.
//
// The hook only covers the top of the loop — it loads memory and stops.
// Everything after that is a judgement the model makes mid-session, which
// no hook can make for it: which domain it is touching, whether a fact is
// worth writing down, whether the work has actually shipped.
//
// The rules are written to produce restraint rather than participation.
// "Remember what you learned" yields a junk drawer, because a model asked to
// capture learnings will always find some; the useful instruction is the one
// that says what not to write and gives a test for it. 08-ai-workflow.md is
// explicit that fifty good memories beat five hundred mediocre ones, and
// that is a property of the instructions, not of the storage.
//
// Numbers come from config.Default() rather than being typed out, so the
// rules cannot drift from the thresholds the engines actually apply.
func memoryRules(intro string) string {
	d := config.Default()
	// Go raw strings cannot contain a backtick, and this block is mostly
	// markdown code spans. Writing it with "~" and swapping at render time
	// keeps the text readable for the people who will edit it, which is the
	// only property that matters in a literal made of prose.
	rules := strings.ReplaceAll(`## Project memory (brAIn)

`+intro+`

### Before changing code in an area

Run ~brain recall --domain <domain>~. What comes back is ranked, and the
ranking is the point:

- **Corrections** are the owner overruling a tool. Non-negotiable. Do not
  re-argue one and do not route around it.
- **Decisions** are settled. Follow them. Reopen one only when its
  revisit condition has actually been met, and then write a new decision
  that supersedes it rather than quietly doing something else.
- **Lessons** are patterns that already cost somebody something. Assume
  they still apply.
- **Facts** go stale after %DAYS% days. Treat an old one as a claim to
  check, not as ground truth.

### Before shipping

Run ~brain trust --domain <domain>~ and act on the level:

- **%ASK%** — stop and get a human to approve the change.
- **%NOTIFY%** — ship it, and say plainly that you did.
- **%AUTOSHIP%** and **%FULLAUTO%** — ship without asking. The domain earned
  this by shipping cleanly, and second-guessing it wastes what it bought.
  Earned autonomy is not permission to skip the recall.

brAIn reports a level and nothing more. It does not block you, and acting on
the level is your job.

### After the work is done

Write down what changed the ground, not what happened:

    brain remember "<what you learned>" --domain <domain> --layer <layer>

Pass ~--layer~ explicitly — ~fact~, ~lesson~, ~decision~, or ~correction~.
Left off, brAIn guesses from the wording, and a guess that files a decision
as a fact makes it expire in %DAYS% days. You know which one it is.

Worth remembering: a codebase fact that changes how the next person
approaches this area; a lesson from a bug that took real time to find; a
decision made in this session that should outlive it.

Not worth remembering: anything the code already says, anything true only
this week ("PR #42 is in review"), anything a competent stranger to this
project would already know, or a summary of what you just did. When in
doubt, leave it out — a missing memory costs one recall, and a junk one
costs the credibility of every memory next to it.

### Trust outcomes are not yours to record

~brain trust record~ belongs to the moment work actually ships: a merge, a
deploy, a production window that stayed quiet. Not the moment you stop
typing. A clean outcome recorded per finished task measures typing rather
than work surviving contact with reality, and it walks a domain up the
ladder to %AUTOSHIP% having shipped nothing.
`, "~", "`")

	return strings.NewReplacer(
		"%DAYS%", strconv.Itoa(d.Facts.StaleAfterDays),
		"%ASK%", string(trust.LevelAsk),
		"%NOTIFY%", string(trust.LevelNotify),
		"%AUTOSHIP%", string(trust.LevelAutoShip),
		"%FULLAUTO%", string(trust.LevelFullAuto),
	).Replace(rules)
}

func sessionStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "session-start",
		Short: "Print project memory for an editor's session-start hook",
		Long:  sessionStartLongHelp,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			memories := sessionStartRecall(cmd)
			if len(memories) == 0 {
				return nil
			}
			fmt.Print(renderSessionStart(memories))
			return nil
		},
	}
}

// sessionStartRecall returns the memories to open a session with, or none.
//
// Every failure path returns nil rather than an error, because the caller is
// a hook whose only failure mode is refusing to start a session. A project
// that has never heard of brAIn, or one where init ran a minute ago, has
// nothing to say — which is not the same as something going wrong.
//
// The engine is built here rather than read from the context: hooks is in
// runsWithoutBrainDir, so it skips engine setup and engineFrom returns nil
// by the contract documented there.
func sessionStartRecall(cmd *cobra.Command) []memory.Memory {
	dirFlag, err := cmd.Flags().GetString("dir")
	if err != nil {
		return nil
	}
	brainDir, err := resolveBrainDir(dirFlag)
	if err != nil {
		return nil
	}
	cfg, err := config.Load(brainDir)
	if err != nil {
		return nil
	}
	eng, err := engine.NewEngine(cmd.Context(), markdown.New(brainDir),
		engine.WithLockDir(brainDir),
		engine.WithFacts(cfg.Facts))
	if err != nil {
		return nil
	}
	memories, err := eng.Recall(cmd.Context(), engine.RecallOptions{Limit: sessionStartLimit})
	if err != nil {
		return nil
	}
	return memories
}

// renderSessionStart frames the memories for a model rather than for a
// human: the ranking is spelled out, because a bare list does not tell a
// reader that a correction outranks a fact.
func renderSessionStart(memories []memory.Memory) string {
	var b strings.Builder
	b.WriteString(sessionStartHeader)
	for _, m := range memories {
		fmt.Fprintf(&b, "[%s] %s\n", m.Layer, m.Title())
		fmt.Fprintf(&b, "  domain: %s  path: %s\n\n", m.Domain, m.Path)
	}
	b.WriteString(sessionStartFooter)
	return b.String()
}
