#!/usr/bin/env bash
# Validates workspace.dsl and regenerates the Mermaid diagrams in README.md.
# Needs Java 21+ and structurizr.war (see README.md). Override with JAVA=... and STRUCTURIZR_WAR=...
set -euo pipefail
cd "$(dirname "$0")"

war="${STRUCTURIZR_WAR:-$HOME/.local/lib/structurizr.war}"
java="${JAVA:-$(/usr/libexec/java_home -v 21+ 2>/dev/null)/bin/java}"
out="$(mktemp -d)"
trap 'rm -rf "$out"' EXIT

"$java" -jar "$war" validate -workspace workspace.dsl
"$java" -jar "$war" export -workspace workspace.dsl -format mermaid -output "$out" > /dev/null

{
  sed '/<!-- diagrams:start -->/q' README.md
  for entry in "SystemContext:System Context (L1)" "RequestPath:RequestPath (L2)" "UsagePipeline:UsagePipeline (L2)"; do
    view="${entry%%:*}"
    printf '\n### %s\n\n```mermaid\n' "${entry#*:}"
    cat "$out/structurizr-$view.mmd"
    printf '\n```\n'
  done
  printf '\n'
  sed -n '/<!-- diagrams:end -->/,$p' README.md
} > README.md.new
mv README.md.new README.md
