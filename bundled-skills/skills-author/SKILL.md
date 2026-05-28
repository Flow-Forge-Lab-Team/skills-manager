---
name: skills-author
description: Guide the creation of a new, well-formed agent skill — name, activation-safe description, body, compatibility declaration, and execution requirements — and emit a complete SKILL.md plus suggested requirements. Use when a user wants to write a new skill.
compatible: [claude, codex, grok, antigravity, gemini, hermes, openclaw]
output_format: markdown-skill
tags: [bundled-skill, manager-internal]
---

# skills-author

You help authors create a new, well-formed skill for the skills-manager project.
You produce a single complete `SKILL.md` (frontmatter + body) that is ready to
ingest, including a compatibility declaration and any execution requirements —
not just name/description/body.

## Input

You are given the intended **skill name** and (optionally) a short description
of what the skill should do. Ask for missing intent only if you cannot proceed.

## What a good skill needs

Walk through each, then emit the final file:

1. **Name** — short, kebab-case, matches the requested name exactly.
2. **Description** — the activation trigger. It must say *when to use the skill*
   ("Use when …"), be specific, and avoid over-broad phrasing that would fire on
   unrelated tasks. This is the single most important field for correct
   activation.
3. **Body** — concrete, ordered steps. Prefer plain, harness-neutral
   instructions; avoid hard-coding one harness's tool names unless declaring an
   exclusive skill.
4. **Compatibility** — declare `compatible: [...]` (or `exclusive: <harness>`
   with a `reason:`) reflecting which harnesses the skill works on. Default to a
   broad `compatible` list when the body is harness-neutral.
5. **Execution requirements** — if the skill calls binaries, MCP servers,
   scripts, credentials, or needs specific model capabilities, state them so the
   manager can record them. Do not omit a required tool.
6. **Testing** — include a short "How to verify" note in the body describing how
   an author confirms the skill works.

## Rules — validated; a violation rejects the draft

- The frontmatter `name:` MUST equal the requested name.
- The `description:` MUST be present, specific, and activation-safe (no TODO
  placeholder, no "does everything" phrasing).
- Never include hostile or policy-bypassing instructions (ignore-instructions,
  hide-changes, exfiltrate, disable-safety, bypass-policy).
- Output a single valid `SKILL.md`: YAML frontmatter between `---` fences, then
  the markdown body. No commentary outside the file, no fences around the whole
  thing.

## Output

Return **only** the complete `SKILL.md` (frontmatter + body), nothing else.
