# Security model

`skills-manager` manages executable agent instructions. Treat every skill like
code you may hand to an automated tool with filesystem and shell access.

## Trust boundary

The manager helps organize, preview, copy, update, and remove skills. It does
not make third-party skill content safe by itself. You are still responsible for
reviewing skill instructions before installing or updating them.

## Install boundary

`skills-manager add` copies a source skill into the local manager library and
records metadata. Local paths are read from disk. GitHub sources are cloned
before ingest. Marketplace-style sources resolve to their configured source
before ingest. The manager does not execute skill content during ingest.

`skills-manager match --explain` previews why a skill matches a project and, in
explain mode, why unrelated catalog entries are rejected. `skills-manager
install` copies only matching skills into configured project harness paths.

## Copy-mode boundary

Copy mode is the default. Project installs are manager-owned copies, not
symlinks into the library. The manager records installed paths and file
fingerprints in manifests and lock files. If a target path already contains
unmanaged content or local edits, install and sync preserve that content instead
of overwriting it.

## Update and scan boundary

Update commands are review-first: pending changes can be inspected before they
replace library content. Automated paths, such as high-confidence scan ingest,
must be requested explicitly. Dependency preflight checks can block install or
ingest when required local tools, credentials, runtimes, MCP servers, or model
capabilities are missing.

Discovery and scan commands require explicit scope. `discover --global` reads
known global tool roots; `discover --projects` and `--saved-project-roots` read
only approved project roots. The manager does not default to a full-home or
arbitrary filesystem scan. Generated directories, Codex scratch workspaces, and
secret-bearing paths such as `.env`, credential files, private keys, and secret
directories are skipped by default.

AI assessment is opt-in. `assess --auto` uses the configured local provider and
`assess --handoff` writes a local prompt for the user's agent; neither mode runs
during discovery. Assessment results are cached under the manager home by
subject, project fingerprint, target harness, prompt version, and provider.
Before provider calls or handoff prompt writes, obvious secret values in skill
and instruction excerpts are replaced with `[REDACTED_SECRET]`, secret-bearing
files are omitted, and repository remote paths are summarized instead of sent
verbatim.

Privacy audit records are best-effort local log entries under
`~/.skills-manager/logs/`. They record command mode and counts, not provider
prompts, secret values, or file contents.

## Uninstall boundary

`skills-manager uninstall` removes manager-owned installed copies and manifests.
It does not remove unmanaged files and does not delete the canonical library
skill. Use uninstall to reverse project installs; remove library entries
separately when you no longer trust or need a skill.

## Release integrity

`install.sh` and the npm wrapper download a release archive and verify its
SHA-256 checksum against `skills-manager_checksums.txt` from the same GitHub
release. This catches corrupted or substituted archives when the checksum file
is the trusted reference.

Checksums alone do not prove publisher authenticity. GoReleaser is configured
to sign the checksum artifact when cosign credentials are present in the release
environment, but the shell installer and npm wrapper do not currently enforce
signature verification. Users who need authenticity verification should inspect
the release page and verify the signed checksum artifact separately before
running the binary.
