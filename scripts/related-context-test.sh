#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if ! command -v jq >/dev/null 2>&1; then
  echo "error: jq is required for this test" >&2
  exit 1
fi

JINN_BIN="${JINN_BIN:-./jinn}"
echo "building jinn binary at $JINN_BIN..."
go build -o "$JINN_BIN" ./cmd/jinn/

TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT
HOME_DIR="$TMP_DIR/home"

# Isolated HOME so results are deterministic and don't depend on user state.
export HOME="$HOME_DIR"
mkdir -p "$HOME_DIR/.claude/kb" "$HOME_DIR/.pi/docs" "$HOME_DIR/.pi/agent/kb" "$HOME_DIR/.pi/agent/skills"

cat > "$HOME_DIR/.claude/kb/base.md" <<'EOF'
# Base KB note

Contains token-claude-kb-marker.
EOF
cat > "$HOME_DIR/.pi/docs/doc.md" <<'EOF'
# Pi Docs note

Contains token-pi-docs-marker.
EOF
cat > "$HOME_DIR/.pi/agent/kb/agent.md" <<'EOF'
# Agent KB note

Contains token-agent-kb-marker.
EOF
cat > "$HOME_DIR/.pi/agent/skills/parity-skill.md" <<'EOF'
---
name: parity-skill
description: Skill match for replacement parity
---
# Parity Skill

Contains token-parity-marker.
EOF
for i in 0 1 2 3 4 5; do
  cat > "$HOME_DIR/.pi/agent/kb/parity-$i.md" <<EOF
# Parity KB $i

Contains token-parity-marker.
EOF
done

EXTRA_KB="$TMP_DIR/extra-agent-kb"
mkdir -p "$EXTRA_KB"
cat > "$EXTRA_KB/extra.md" <<'EOF'
# Extra KB note

Contains token-extra-marker.
EOF

run_query() {
  local cfg_dir=$1
  local query=$2
  local payload

  payload=$(jq -cn --arg q "$query" '{
    tool: "related_context",
    args: {
      client: "pi",
      query: $q,
      types: ["kb"],
      limit: 10,
      rebuild: true,
    },
  }')

  JINN_CONFIG_DIR="$cfg_dir" "$JINN_BIN" <<<"$payload"
}

run_skill_kb_query() {
  local cfg_dir=$1
  local query=$2
  local limit=$3
  local payload

  payload=$(jq -cn --arg q "$query" --argjson limit "$limit" '{
    tool: "related_context",
    args: {
      client: "pi",
      query: $q,
      types: ["skill", "kb"],
      limit: $limit,
      rebuild: true,
    },
  }')

  JINN_CONFIG_DIR="$cfg_dir" "$JINN_BIN" <<<"$payload"
}

extract() {
  local json=$1
  local jq_expr=$2
  jq -r "$jq_expr" <<<"$json"
}

extract_total() {
  local json=$1
  extract "$json" '.result | fromjson | .results | length'
}

extract_source_count() {
  local json=$1
  extract "$json" '.result | fromjson | .index.source_count'
}

extract_contains_path() {
  local json=$1
  local needle=$2
  if extract "$json" '.result | fromjson | .results[].path' | grep -qF "$needle"; then
    echo yes
  else
    echo no
  fi
}

extract_type_count() {
  local json=$1
  local want_type=$2
  extract "$json" '.result | fromjson | .results[] | .type' | grep -c "^${want_type}$" || true
}

print_counts() {
  local label=$1
  local cfg=$2
  local -a queries=(
    token-claude-kb-marker
    token-pi-docs-marker
    token-agent-kb-marker
  )

  echo
  echo "== $label =="
  for q in "${queries[@]}"; do
    local out total source
    out=$(run_query "$cfg" "$q")
    total=$(extract_total "$out")
    source=$(extract_source_count "$out")
    printf '%-12s total=%s source_count=%s\n' "$q" "$total" "$source"
  done
}

assert_or_fail() {
  local cond=$1
  local message=$2
  if ! eval "$cond"; then
    echo "FAIL: $message" >&2
    exit 1
  fi
}

mkdir -p "$TMP_DIR/base/jinn" "$TMP_DIR/with-extra/jinn"
cat > "$TMP_DIR/base/jinn/config.json" <<'JSON'
{
  "related_context": {
    "paths": [
      "~/.claude/kb/"
    ]
  }
}
JSON
cat > "$TMP_DIR/with-extra/jinn/config.json" <<JSON
{
  "related_context": {
    "paths": [
      "~/.claude/kb/",
      "${EXTRA_KB}"
    ]
  }
}
JSON

print_counts "Config base" "$TMP_DIR/base"
print_counts "Config with extra" "$TMP_DIR/with-extra"

assertion_rows=(
  "token-claude-kb-marker" 
  "token-pi-docs-marker" 
  "token-agent-kb-marker"
)

echo

echo "== Equal-or-better coverage matrix =="
echo "Query                              base_total with_total base_sources with_sources base_hit extra_hit"
echo "-----------------------------------------------------------------"
for q in "${assertion_rows[@]}"; do
  base_out=$(run_query "$TMP_DIR/base" "$q")
  with_out=$(run_query "$TMP_DIR/with-extra" "$q")
  bt=$(extract_total "$base_out")
  wt=$(extract_total "$with_out")
  bs=$(extract_source_count "$base_out")
  ws=$(extract_source_count "$with_out")
  case "$q" in
    token-claude-kb-marker)
      expected_path="/.claude/kb/"
      ;;
    token-pi-docs-marker)
      expected_path="/.pi/docs/"
      ;;
    token-agent-kb-marker)
      expected_path="/.pi/agent/kb/"
      ;;
    *)
      expected_path=""
      ;;
  esac

  base_has_source="$(extract_contains_path "$base_out" "$expected_path")"
  with_has_source="$(extract_contains_path "$with_out" "$expected_path")"

  printf '%-33s %-10s %-10s %-11s %-11s %-9s %-9s\n' \
    "$q" "$bt" "$wt" "$bs" "$ws" "$base_has_source" "$with_has_source"

  if [ "$wt" -lt "$bt" ]; then
    echo "FAIL: with-extra total for $q should be >= base total" >&2
    exit 1
  fi
  if [ "$ws" -le "$bs" ]; then
    echo "FAIL: with-extra source_count for $q should be > base source_count" >&2
    exit 1
  fi
  if [ "$base_has_source" != "yes" ]; then
    echo "FAIL: base run for $q should include expected source $expected_path" >&2
    exit 1
  fi
  if [ "$with_has_source" != "yes" ]; then
    echo "FAIL: with-extra run for $q should include expected source $expected_path" >&2
    exit 1
  fi
done

BASE_AGENT_OUT=$(run_query "$TMP_DIR/base" "token-agent-kb-marker")
BASE_DOCS_OUT=$(run_query "$TMP_DIR/base" "token-pi-docs-marker")
BASE_CLAUDE_OUT=$(run_query "$TMP_DIR/base" "token-claude-kb-marker")
WITH_OUT=$(run_query "$TMP_DIR/with-extra" "token-extra-marker")

BASE_AGENT_TOTAL=$(extract_total "$BASE_AGENT_OUT")
BASE_DOCS_TOTAL=$(extract_total "$BASE_DOCS_OUT")
BASE_CLAUDE_TOTAL=$(extract_total "$BASE_CLAUDE_OUT")
WITH_TOTAL=$(extract_total "$WITH_OUT")
BASE_SOURCE=$(extract_source_count "$BASE_AGENT_OUT")
WITH_SOURCE=$(extract_source_count "$WITH_OUT")
BASE_HAS_PI_DOCS=$(extract_contains_path "$BASE_DOCS_OUT" "/.pi/docs/")
BASE_HAS_AGENT_KB=$(extract_contains_path "$BASE_AGENT_OUT" "/.pi/agent/kb/")
BASE_HAS_CLAUDE_KB=$(extract_contains_path "$BASE_CLAUDE_OUT" "/.claude/kb/")
WITH_HAS_EXTRA=$(extract_contains_path "$WITH_OUT" "/extra-agent-kb/")

echo

echo "== Regression checks =="
echo "agent kb token: base_total=$BASE_AGENT_TOTAL base_source_count=$BASE_SOURCE with_total=$WITH_TOTAL with_source_count=$WITH_SOURCE"
echo "== Source inclusion checks =="
echo "pi docs in base: $BASE_HAS_PI_DOCS"
echo "agent kb in base: $BASE_HAS_AGENT_KB"
echo "claude kb in base: $BASE_HAS_CLAUDE_KB"

assert_or_fail "[ \"$BASE_AGENT_TOTAL\" -ge 1 ]" "expected agent kb marker to be discovered from default pi sources"
assert_or_fail "[ \"$BASE_DOCS_TOTAL\" -ge 1 ]" "expected pi docs marker to be discovered from default pi sources"
assert_or_fail "[ \"$BASE_CLAUDE_TOTAL\" -ge 1 ]" "expected claude kb marker to be discovered from default pi sources"
assert_or_fail "[ \"$BASE_HAS_PI_DOCS\" = yes ]" "expected pi docs source to be included by default"
assert_or_fail "[ \"$BASE_HAS_AGENT_KB\" = yes ]" "expected agent kb source to be included by default"
assert_or_fail "[ \"$BASE_HAS_CLAUDE_KB\" = yes ]" "expected claude kb source to be included by default"
assert_or_fail "[ \"$WITH_TOTAL\" -ge 1 ]" "expected token-extra-marker from configured extra path"
assert_or_fail "[ \"$WITH_HAS_EXTRA\" = yes ]" "expected match from configured extra path"
assert_or_fail "[ \"$WITH_SOURCE\" -gt \"$BASE_SOURCE\" ]" "expected source_count to grow when extra source is added"

PARITY_OUT=$(run_skill_kb_query "$TMP_DIR/base" "token-parity-marker" 5)
PARITY_SKILLS=$(extract_type_count "$PARITY_OUT" "skill")
PARITY_KB=$(extract_type_count "$PARITY_OUT" "kb")
PARITY_TOTAL=$(extract_total "$PARITY_OUT")

echo

echo "== Replacement parity check =="
echo "skill+kb query with limit=5: skill=$PARITY_SKILLS kb=$PARITY_KB total=$PARITY_TOTAL"
assert_or_fail "[ \"$PARITY_SKILLS\" -ge 1 ]" "expected at least one skill match in mixed skill+kb query"
assert_or_fail "[ \"$PARITY_KB\" -eq 5 ]" "expected five kb matches in mixed skill+kb query"
assert_or_fail "[ \"$PARITY_TOTAL\" -eq 6 ]" "expected 1 skill + 5 kb matches in mixed query fixture"

echo "PASS: related_context coverage regression test passed"
