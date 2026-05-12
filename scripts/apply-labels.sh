#!/usr/bin/env bash
# Idempotent label applier for ringo380/ccmcp.
# Reads .github/labels.yml and runs `gh label create --force` for each entry.
# Requires: gh, yq (or python3 with PyYAML).

set -u

REPO="${REPO:-ringo380/ccmcp}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
LABELS="$ROOT/.github/labels.yml"

if [ ! -f "$LABELS" ]; then
  echo "labels file not found: $LABELS" >&2
  exit 1
fi

# Parse YAML via python3 (no yq dependency)
python3 - "$LABELS" <<'PY' | while IFS=$'\t' read -r name color desc; do
import sys, yaml
with open(sys.argv[1]) as f:
    for item in yaml.safe_load(f):
        print(f"{item['name']}\t{item['color']}\t{item['description']}")
PY
  if [ -z "${name:-}" ]; then continue; fi
  echo "→ $name"
  gh label create "$name" \
    --repo "$REPO" \
    --color "$color" \
    --description "$desc" \
    --force >/dev/null
done

echo "Done."
