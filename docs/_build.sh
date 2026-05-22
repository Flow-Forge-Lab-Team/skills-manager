#!/usr/bin/env bash
# Build script: convert all .md docs to styled HTML using shared template.
# Idempotent — safe to re-run after edits.

set -euo pipefail

cd "$(dirname "$0")"

# Docs in this directory.
docs=(VISION ARCHITECTURE ROADMAP DATA_MODEL CLI_REFERENCE TAXONOMY \
      COMPATIBILITY INGEST_FLOW UPDATE_FLOW BUNDLED_SKILLS SCHEDULING CROSS_MACHINE)

# Pretty titles for each doc (used in <title> + nav).
declare -A titles=(
  [VISION]="Vision"
  [ARCHITECTURE]="Architecture"
  [ROADMAP]="Roadmap"
  [DATA_MODEL]="Data model"
  [CLI_REFERENCE]="CLI reference"
  [TAXONOMY]="Taxonomy"
  [COMPATIBILITY]="Compatibility"
  [INGEST_FLOW]="Ingest flow"
  [UPDATE_FLOW]="Update flow"
  [BUNDLED_SKILLS]="Bundled skills"
  [SCHEDULING]="Scheduling"
  [CROSS_MACHINE]="Cross-machine sync"
)

rewrite_links() {
  # Rewrite href="X.md" → href="X.html" but only for files in the docs/ directory.
  # External .md references (like SKILL.md, agentskills.io) stay untouched because they
  # don't appear as href targets.
  local file="$1"
  for d in "${docs[@]}" INDEX; do
    sed -i '' "s|href=\"${d}.md\"|href=\"${d}.html\"|g" "$file"
    sed -i '' "s|href=\"./${d}.md\"|href=\"${d}.html\"|g" "$file"
  done
  # README at repo root.
  sed -i '' "s|href=\"../README.md\"|href=\"../README.html\"|g" "$file"
  sed -i '' "s|href=\"README.md\"|href=\"README.html\"|g" "$file"
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
  sed -i '' "s|href=\"${key}.html\">view source (.md)|href=\"${key}.md\">view source (.md)|g" "${key}.html"
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
sed -i '' 's|href="INDEX.html">view source (.md)|href="INDEX.md">view source (.md)|g' index.html
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
  sed -i '' "s|href=\"docs/${d}.md\"|href=\"docs/${d}.html\"|g" ../README.html
done

# Fix README's brand link + nav links: README sits one level above docs/.
# Template uses relative paths assuming we're inside docs/. README needs docs/ prefix.
sed -i '' \
  -e 's|href="index.html"|href="docs/index.html"|g' \
  -e 's|href="VISION.html"|href="docs/VISION.html"|g' \
  -e 's|href="ARCHITECTURE.html"|href="docs/ARCHITECTURE.html"|g' \
  -e 's|href="ROADMAP.html"|href="docs/ROADMAP.html"|g' \
  -e 's|href="DATA_MODEL.html"|href="docs/DATA_MODEL.html"|g' \
  -e 's|href="CLI_REFERENCE.html"|href="docs/CLI_REFERENCE.html"|g' \
  -e 's|href="TAXONOMY.html"|href="docs/TAXONOMY.html"|g' \
  -e 's|href="COMPATIBILITY.html"|href="docs/COMPATIBILITY.html"|g' \
  -e 's|href="INGEST_FLOW.html"|href="docs/INGEST_FLOW.html"|g' \
  -e 's|href="UPDATE_FLOW.html"|href="docs/UPDATE_FLOW.html"|g' \
  -e 's|href="BUNDLED_SKILLS.html"|href="docs/BUNDLED_SKILLS.html"|g' \
  -e 's|href="SCHEDULING.html"|href="docs/SCHEDULING.html"|g' \
  -e 's|href="CROSS_MACHINE.html"|href="docs/CROSS_MACHINE.html"|g' \
  -e 's|href="../mockup.html"|href="mockup.html"|g' \
  -e 's|href="../README.html"|href="README.html"|g' \
  -e 's|href="../README.md"|href="README.html"|g' \
  -e 's|href="styles.css"|href="docs/styles.css"|g' \
  ../README.html

sed -i '' 's|href="README.html">view source (.md)|href="README.md">view source (.md)|g' ../README.html

printf "  ✓ ../README.html\n"
echo "Done."
