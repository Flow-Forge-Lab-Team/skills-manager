# Design docs index

Read in this order. The first four (★) are the foundation; the rest cover specific subsystems.

## Source of truth

The Markdown files are canonical. The adjacent `.html` files are generated for
human browsing by running:

```bash
./docs/_build.sh
```

Do not hand-edit the generated HTML except to change the shared template or
styles.

`docs/_build.sh` is the canonical committed-HTML path. `mkdocs.yml` is retained
for future hosted docs experiments, but the GitHub repository currently has no
published docs homepage; do not claim a public docs site until a reachable URL is
configured.

## Foundation

- ★ [VISION.md](VISION.md) — problem, solution, who it's for, differentiation, non-goals
- ★ [ARCHITECTURE.md](ARCHITECTURE.md) — CLI-first system shape, components, principles
- ★ [ROADMAP.md](ROADMAP.md) — v0.1 / v0.2 / v0.3 / v1.0 staging
- ★ [DATA_MODEL.md](DATA_MODEL.md) — file formats, schemas, on-disk layout

## Surface

- [CLI_REFERENCE.md](CLI_REFERENCE.md) — every command, flag, exit code
- [TAXONOMY.md](TAXONOMY.md) — 10 categories + flat tags + matching algorithm

## Subsystems

- [COMPATIBILITY.md](COMPATIBILITY.md) — compatibility + execution requirements, variants
- [INGEST_FLOW.md](INGEST_FLOW.md) — adding skills (5 sources, scan-first ingest, optional watcher)
- [UPDATE_FLOW.md](UPDATE_FLOW.md) — origin tracking, polling, diff + AI summary
- [BUNDLED_SKILLS.md](BUNDLED_SKILLS.md) — manager skills (port, ingest, summary, etc.)
- [SCHEDULING.md](SCHEDULING.md) — local OS scheduling; cloud scheduling deferred
- [CROSS_MACHINE.md](CROSS_MACHINE.md) — git-based library sync, drift detection

## Out-of-band

- [ACCEPTANCE_FLO_242.md](ACCEPTANCE_FLO_242.md) — v0.1 realistic-project acceptance smoke
- [/mockup.html](../mockup.html) — clickable UI mockup (open in browser)
