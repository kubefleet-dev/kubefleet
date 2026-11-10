# Skills

Skills are on-demand procedures that AI coding assistants load when a task matches their description. They extend [`AGENTS.md`](../../AGENTS.md), which every assistant loads always, with the multi-step jobs that would bloat it: CVE remediation, e2e failure analysis, API changes, releases, backports, and the maintainers' review rubric.

Tools that implement the [Agent Skills specification](https://agentskills.io) read this directory; `.claude/skills` is a symlink here for the one mainstream tool that looks there instead. That symlink and the one-line `CLAUDE.md` are the only vendor-specific files this directory needs; `.github/copilot-instructions.md` is a pointer to `AGENTS.md` for the same reason. A new shim is added only when a tool that contributors here actually use does not read the standard. The maintainers' review rubric is the `review` skill rather than a per-tool agent, so every tool reads one file. Squad's own skills live separately in `.github/skills/` and are managed by `squad upgrade`.

## When to add a skill

Add one when a procedure is too long for a line in `AGENTS.md`, is only needed for a specific kind of task, and has been explained to a contributor at least twice. Do not add one for a one-line rule (put it in `AGENTS.md`), for something every session needs (same), or for a library's usage (link the docs).

## Layout

```text
.agents/skills/
  <name>/
    SKILL.md        required; frontmatter plus the procedure
    <other>.md      optional references, linked from SKILL.md, one level deep
```

Keep the directory flat: some tools do not discover nested skill folders.

## Frontmatter

```yaml
---
name: e2e-failure-analysis            # lowercase, hyphens, must equal the directory name
description: One paragraph. What it does, the concrete triggers ("Use when ..."), and what it is not for if a neighbouring skill is close. This is what the assistant matches on.
license: Apache-2.0
metadata:
  author: The KubeFleet Authors
---
```

`name` and `description` are required by the [Agent Skills specification](https://agentskills.io/specification); the rest is optional, and the `skills-ref` validator (below) rejects fields the specification does not list. A skill that only a human may start (the `release` skill) says so in its description and its first paragraph, since there is no portable field for it. Names must not collide with the skills under `.github/skills/`.

## Writing one

- Under 200 lines. Commands copied from the `Makefile` or a workflow, never typed from memory; a wrong command in a skill becomes a wrong action in every future session.
- Structure as numbered steps. Verification comes before any step that pushes, tags, labels, or opens a PR, and that step starts with a stop for human confirmation.
- Point at `AGENTS.md` for a rule it already states; restate only the part a reader of this skill acts on differently (the exception, the command, the trap).
- Say where the source of truth is (a workflow, a Makefile target, a doc) so a reader can check the skill when it ages.
- Validate before opening a PR: `npx -y skills-ref validate .agents/skills/<name>`. CI does not run it; it is a manual step.
- A skill change is a `docs:` PR with `release-note/docs`.
