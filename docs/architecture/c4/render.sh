#!/usr/bin/env bash
# Validates workspace.dsl and the generated code workspaces, and regenerates
# the Mermaid diagrams in README.md.
#
#   ./render.sh           rewrite README.md
#   ./render.sh --check   fail if README.md does not match workspace.dsl (CI)
#
# Structurizr runs as `java -jar structurizr.war` (Java 21+, see README.md;
# override with JAVA=... and STRUCTURIZR_WAR=...), or as a Docker image when
# STRUCTURIZR_IMAGE is set (CI pins it by digest).
set -euo pipefail
cd "$(dirname "$0")"

check=false
if [[ "${1:-}" == "--check" ]]; then
  check=true
fi

# The output folder is inside this folder, so the Docker image can write it.
out=".render"
rm -rf "$out"
mkdir -p "$out"
trap 'rm -rf "$out" README.md.new' EXIT

structurizr() {
  if [[ -n "${STRUCTURIZR_IMAGE:-}" ]]; then
    docker run --rm -u "$(id -u):$(id -g)" -v "$PWD:/usr/local/structurizr" -w /usr/local/structurizr \
      "$STRUCTURIZR_IMAGE" "$@"
  else
    local war="${STRUCTURIZR_WAR:-$HOME/.local/lib/structurizr.war}"
    local java="${JAVA:-$(/usr/libexec/java_home -v 21+ 2>/dev/null)/bin/java}"
    "$java" -jar "$war" "$@"
  fi
}

structurizr validate -workspace workspace.dsl
for ws in generated/*.code.dsl; do
  structurizr validate -workspace "$ws"
done
structurizr export -workspace workspace.dsl -format mermaid -output "$out" > /dev/null

{
  sed '/<!-- diagrams:start -->/q' README.md
  for entry in \
    "SystemContext:System Context (L1)" \
    "RequestPath:RequestPath (L2)" \
    "UsagePipeline:UsagePipeline (L2)" \
    "UsageWriteAPIComponents:UsageWriteAPI components (L3, generated)" \
    "UsageWriterComponents:Usage Writer components (L3, generated)" \
    "ExampleAppComponents:ExampleApp components (L3, generated)"; do
    view="${entry%%:*}"
    printf '\n### %s\n\n```mermaid\n' "${entry#*:}"
    cat "$out/structurizr-$view.mmd"
    printf '\n```\n'
  done
  printf '\n'
  sed -n '/<!-- diagrams:end -->/,$p' README.md
} > README.md.new

if $check; then
  if ! diff -u README.md README.md.new; then
    echo "docs/architecture/c4/README.md is out of date: run docs/architecture/c4/render.sh" >&2
    exit 1
  fi
else
  mv README.md.new README.md
fi
