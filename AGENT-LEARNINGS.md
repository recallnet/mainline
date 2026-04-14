# Agent Learnings

Durable directives from AI agent sessions. Newest first.
Long investigations belong in `docs/observations/`, not here.

Each entry includes a directive: a concrete "Do X, not Y" instruction.
Entries marked `hypothesis` have not been independently verified.
Entries tagged `meta` are cross-cutting (not repo-specific) and surfaced team-wide.

## Format

Each field on its own line, prefixed with the field name and colon.

```
### YYYY-MM-DD — Summary sentence (confirmed, gotcha)

Author: [name]
Insight: One sentence — why this matters and what it changes.
Detail: Specific context, evidence, or mechanism.
Directive: Do X, not Y.
Applies To: [paths, systems, or workflows this affects]
Action: What should change in the machine. "No machine change needed" if directive is sufficient.
Context: branch, what was being done
```

Types: `gotcha` | `dead-end` | `fragile-area` | `codebase-state` | `tool-quirk`

Heading tag format: `(confidence, type)` or `(confidence, type, meta)`.
Type may be omitted for simplified entries: `(confidence)`.

Examples: `(confirmed, gotcha)`, `(hypothesis, dead-end)`, `(confirmed, tool-quirk, meta)`.

Meta entries may add optional fields: `Scope:` (repo | team | domain | general),
`Decision Point:` (what decision this learning would change).

---

<!-- Entries below, newest first -->
