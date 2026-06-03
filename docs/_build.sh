#!/usr/bin/env bash
# Build script: convert all .md docs to styled HTML using shared template.
# Idempotent — safe to re-run after edits.

set -euo pipefail

cd "$(dirname "$0")"

# Docs in this directory.
docs=(TUTORIAL VISION ARCHITECTURE ROADMAP DATA_MODEL DISCOVERY LOADING_COSTS SETUP_WIZARD CLI_REFERENCE TAXONOMY \
      COMPATIBILITY INGEST_FLOW UPDATE_FLOW BUNDLED_SKILLS SCHEDULING CROSS_MACHINE \
      USAGE_TRACKING SECURITY_MODEL RELEASE_CHECKLIST ACCEPTANCE_FLO_242)

# Pretty titles for each doc (used in <title> + nav).
declare -A titles=(
  [VISION]="Vision"
  [TUTORIAL]="5-minute tutorial"
  [ARCHITECTURE]="Architecture"
  [ROADMAP]="Roadmap"
  [DATA_MODEL]="Data model"
  [DISCOVERY]="Discover-first inventory"
  [LOADING_COSTS]="Skill loading cost model"
  [SETUP_WIZARD]="First-run setup wizard"
  [CLI_REFERENCE]="CLI reference"
  [TAXONOMY]="Taxonomy"
  [COMPATIBILITY]="Compatibility"
  [INGEST_FLOW]="Ingest flow"
  [UPDATE_FLOW]="Update flow"
  [BUNDLED_SKILLS]="Bundled skills"
  [SCHEDULING]="Scheduling"
  [CROSS_MACHINE]="Cross-machine sync"
  [USAGE_TRACKING]="Usage tracking"
  [SECURITY_MODEL]="Security model"
  [RELEASE_CHECKLIST]="Release checklist"
  [ACCEPTANCE_FLO_242]="FLO-242 acceptance smoke"
)

rewrite_links() {
  # Rewrite href="X.md" → href="X.html" but only for files in the docs/ directory.
  # External .md references (like SKILL.md, agentskills.io) stay untouched because they
  # don't appear as href targets.
  local file="$1"
  for d in "${docs[@]}" INDEX; do
    edit_in_place "s|href=\"${d}.md\"|href=\"${d}.html\"|g" "$file"
    edit_in_place "s|href=\"./${d}.md\"|href=\"${d}.html\"|g" "$file"
  done
  # README at repo root.
  edit_in_place "s|href=\"../README.md\"|href=\"../README.html\"|g" "$file"
  edit_in_place "s|href=\"README.md\"|href=\"README.html\"|g" "$file"
}

edit_in_place() {
  local expr="$1"
  local file="$2"
  local tmp
  tmp=$(mktemp)
  sed "$expr" "$file" > "$tmp"
  mv "$tmp" "$file"
}

build_doc() {
  local key="$1"
  local title="${titles[$key]}"
  local lower
  lower=$(echo "$key" | tr '[:upper:]' '[:lower:]')

  pandoc "${key}.md" \
    --template=_template.html \
    --from=gfm \
    --to=html5 \
    --metadata pagetitle="${title}" \
    --metadata "source-name=${key}.md" \
    --metadata "active-${lower}=true" \
    -o "${key}.html"

  rewrite_links "${key}.html"
  edit_in_place "s|href=\"${key}.html\">view source (.md)|href=\"${key}.md\">view source (.md)|g" "${key}.html"
  printf "  ✓ %s.html\n" "$key"
}

echo "Building docs/..."
echo "Building docs/index.html..."
pandoc INDEX.md \
  --template=_template.html \
  --from=gfm \
  --to=html5 \
  --metadata pagetitle="Design docs index" \
  --metadata "source-name=INDEX.md" \
  -o index.html

rewrite_links index.html
edit_in_place 's|href="INDEX.html">view source (.md)|href="INDEX.md">view source (.md)|g' index.html
printf "  ✓ index.html\n"

for d in "${docs[@]}"; do
  build_doc "$d"
done

# README sits at repo root, links docs/X.md → docs/X.html.
echo "Building README.html..."
pandoc ../README.md \
  --template=_template.html \
  --from=gfm \
  --to=html5 \
  --metadata pagetitle="README" \
  --metadata "source-name=../README.md" \
  -o ../README.html

# Rewrite README links: docs/X.md → docs/X.html
for d in "${docs[@]}" INDEX; do
  edit_in_place "s|href=\"docs/${d}.md\"|href=\"docs/${d}.html\"|g" ../README.html
done

# Fix README's brand link + nav links: README sits one level above docs/.
# Template uses relative paths assuming we're inside docs/. README needs docs/ prefix.
edit_in_place 's|href="index.html"|href="docs/index.html"|g' ../README.html
edit_in_place 's|href="VISION.html"|href="docs/VISION.html"|g' ../README.html
edit_in_place 's|href="ARCHITECTURE.html"|href="docs/ARCHITECTURE.html"|g' ../README.html
edit_in_place 's|href="ROADMAP.html"|href="docs/ROADMAP.html"|g' ../README.html
edit_in_place 's|href="DATA_MODEL.html"|href="docs/DATA_MODEL.html"|g' ../README.html
edit_in_place 's|href="DISCOVERY.html"|href="docs/DISCOVERY.html"|g' ../README.html
edit_in_place 's|href="CLI_REFERENCE.html"|href="docs/CLI_REFERENCE.html"|g' ../README.html
edit_in_place 's|href="TAXONOMY.html"|href="docs/TAXONOMY.html"|g' ../README.html
edit_in_place 's|href="COMPATIBILITY.html"|href="docs/COMPATIBILITY.html"|g' ../README.html
edit_in_place 's|href="INGEST_FLOW.html"|href="docs/INGEST_FLOW.html"|g' ../README.html
edit_in_place 's|href="UPDATE_FLOW.html"|href="docs/UPDATE_FLOW.html"|g' ../README.html
edit_in_place 's|href="BUNDLED_SKILLS.html"|href="docs/BUNDLED_SKILLS.html"|g' ../README.html
edit_in_place 's|href="SCHEDULING.html"|href="docs/SCHEDULING.html"|g' ../README.html
edit_in_place 's|href="CROSS_MACHINE.html"|href="docs/CROSS_MACHINE.html"|g' ../README.html
edit_in_place 's|href="../mockup.html"|href="mockup.html"|g' ../README.html
edit_in_place 's|href="../README.html"|href="README.html"|g' ../README.html
edit_in_place 's|href="../README.md"|href="README.html"|g' ../README.html
edit_in_place 's|href="styles.css"|href="docs/styles.css"|g' ../README.html

edit_in_place 's|href="README.html">view source (.md)|href="README.md">view source (.md)|g' ../README.html

printf "  ✓ ../README.html\n"
echo "Done."
