# c4sync

c4sync reads a Go service and writes its component (C3) and code (C4) diagrams as Structurizr DSL. It also checks that the committed diagrams still match the code, and it reports what changed in the structure between two versions of the code.

The point is review. You read the diagrams and the structural diff, and go to the code only where they show something unexpected.

c4sync writes DSL only. Structurizr CLI validates it and renders it to Mermaid, PlantUML or anything else Structurizr exports.

## How it works

```
.go-arch-lint.yml ──► go-arch-lint mapping ──┐
                                             ├──► model ──► DSL files ──► Structurizr CLI ──► Mermaid
Go source ──► go/packages + go/types ────────┤
c4sync.yml ──► detectors, names, externals ──┘
```

1. **Components come from go-arch-lint.** The `components` section of `.go-arch-lint.yml` says which packages form which component. c4sync runs `go-arch-lint mapping` and uses its answer. It never decides on its own where a component starts. The same file holds the dependency rules, and `c4sync check` runs them.
2. **Relations come from the code.** c4sync loads the module with `go/packages` and type information, without building or running the service:
   - an import from one component's package into another's is a relation between the two components;
   - an import of a library listed under `externals` is a relation to a workspace element, such as the database;
   - inside a component, every named type is a code element. Struct fields, embedding, method signatures and `implements` become code relations. `implements` is found even when the two packages do not import each other.
3. **Detectors add what the imports do not show:** HTTP routes a component serves, message topics it produces or consumes, and SQL tables it reads, writes or defines. A value that is a constant becomes a name. A value built at runtime is shown as `? (in FuncName)` so that you can see it exists.
4. **Writers turn the model into two files:**
   - a C3 fragment that you `!include` inside the service's container in your own `workspace.dsl`;
   - a standalone code workspace with one view per component.

Every line points back to the code: relations carry `file:line` in `check` and `diff` output. The generated files carry no line numbers, so moving code around does not make them stale.

## Install

c4sync lives in this repository as its own Go module. go-arch-lint is pinned as a Go tool of that module, so there is nothing else to install.

```bash
go -C tools/c4sync build -o "$PWD/bin/c4sync" .
```

```bash
export C4SYNC_GO_ARCH_LINT="$(go -C tools/c4sync tool -n go-arch-lint)"
```

c4sync finds go-arch-lint in this order: the `-go-arch-lint` flag, the `C4SYNC_GO_ARCH_LINT` variable, then `go-arch-lint` on `PATH`.

To render diagrams you also need Java 21+ and the Structurizr CLI (`structurizr.war`). You do not need them for `generate`, `check` or `diff`.

## Set up a service

A service needs two files next to its `go.mod`.

### `.go-arch-lint.yml`: components and rules

```yaml
version: 3
workdir: .
excludeFiles: ["^.*_test\\.go$"]
exclude: [internal/testpg]          # test helpers: not a component, not "unmapped"
components:
  cmd:      { in: cmd/** }
  writeapi: { in: internal/writeapi }
  writer:   { in: internal/writer }
  store:    { in: internal/store }
  core:     { in: internal/core }
commonComponents: [core]
deps:
  cmd:      { anyProjectDeps: true, anyVendorDeps: true }
  writer:   { mayDependOn: [store] }
  store:    { canUse: [pgx] }
```

Two go-arch-lint rules are easy to miss:

- A component that spans several packages (`in: internal/app/**`) must list itself in `mayDependOn`. Otherwise go-arch-lint reports an import between its own packages as a violation.
- A component that is declared but has no packages yet is fine. c4sync draws no box for it until its package exists.

The full syntax is in the [go-arch-lint docs](https://github.com/fe3dback/go-arch-lint).

### `c4sync.yml`: names, externals, detectors, outputs

All paths are relative to this file.

```yaml
name: Usage                       # title of the code workspace
projectPath: .                    # Go module root (default ".")
archFile: .go-arch-lint.yml       # default: .go-arch-lint.yml in projectPath
identifierPrefix: usage           # every DSL identifier starts with it (default "c")
output:
  components: c4/usage.components.dsl   # C3 fragment; empty = do not write
  code: c4/usage.code.dsl               # code workspace; empty = do not write
containers:                       # optional: one module, several binaries
  - name: Writer                  # header of the fragment; default prefix usage_writer
    main: cmd/writer              # main package, relative to the module root
    output: c4/usage.writer.dsl   # C3 fragment of this binary
code:
  maxElements: 30                 # types per code view (default 30)
  maxMethods: 6                   # methods listed per type (default 6)

components:                       # optional C4 metadata; keys are go-arch-lint names
  writer:
    name: Writer
    description: Consumes usage events, drops duplicates, writes batches.
    technology: Go

externals:                        # imports that mean "talks to this element"
  - import: github.com/jackc/pgx  # import path prefix
    element: s.db                 # identifier in your workspace.dsl
    technology: pgx               # default: the import prefix
  - import: github.com/twmb/franz-go
    element: s.kafka
    technology: franz-go

detectors:
  http: true                      # net/http Handle, HandleFunc, ServeMux
  topics:
    element: s.kafka
    produce:
      - call: github.com/twmb/franz-go/pkg/kgo.DefaultProduceTopic
        arg: 0                    # argument index; -1 = the variadic argument and everything after it
      - field: github.com/twmb/franz-go/pkg/kgo.Record.Topic   # a struct literal field
    consume:
      - call: github.com/twmb/franz-go/pkg/kgo.ConsumeTopics
        arg: -1
  sql:
    element: s.db
    calls:
      - call: github.com/jackc/pgx/v5/pgxpool.Pool.Exec   # the part after the package may use * and ?
        arg: 1
      - call: github.com/jackc/pgx/v5.*.Query
        arg: 1
    migrations: [internal/migrations]  # *.sql files; CREATE TABLE lines belong to that package's component
    ignore: [aclexplode]               # names that are not tables

important: [store]                # diff lists changes touching these first
```

A module that builds several binaries is drawn as one container per binary. For each entry under `containers`, c4sync writes a fragment with only the components that the main package imports, directly or through other packages. A component used by two binaries, such as a shared `core`, appears in both fragments, because it is compiled into both. The rules, `check` and `diff` still cover the whole module.

A component without metadata is still drawn, under its go-arch-lint name. A key in `components` or `important` that `.go-arch-lint.yml` does not declare is an error that names the line.

### `workspace.dsl`: put the fragment in its container

```
workspace {
    !identifiers hierarchical
    model {
        s = softwareSystem "Platform" {
            db = container "Usage DB" "" "PostgreSQL"
            kafka = container "Kafka"
            usage = container "Usage service" "" "Go" {
                !include ../../apps/usage/c4/usage.components.dsl
            }
        }
    }
    views {
        component s.usage "usage-components" {
            include *
            autoLayout lr
        }
    }
}
```

The identifiers under `externals` and `detectors` (`s.db`, `s.kafka`) must exist in this workspace. That is why it needs `!identifiers hierarchical`.

## Commands

All commands take `-config` (default `c4sync.yml`) and `-go-arch-lint`.

### `generate`

```bash
c4sync generate -config apps/usage/c4sync.yml
```

```
wrote apps/usage/c4/usage.code.dsl
wrote apps/usage/c4/usage.components.dsl
unmapped package: github.com/…/apps/usage/internal/contracttest
```

An unmapped package belongs to no component. `generate` still writes the files, and `check` fails on it. Either add the package to a component or `exclude` it in `.go-arch-lint.yml`.

The C3 fragment looks like this:

```
# Generated by c4sync from Fixture. Do not edit.
fx_store = component "Store" "" "database/sql" "c4sync" {
    properties {
        "go.packages" "internal/store"
        "table.reads" "events, tenants"
        "table.writes" "events"
    }
}
fx_store -> fx_app "Uses" "Go import" "c4sync"
fx_store -> sys.db "Reads tables: events, tenants" "" "c4sync"
fx_bus -> sys.broker "Produces topics: ? (in Announce), dead-letter" "" "c4sync"
```

The output is sorted and deterministic. The same code always gives the same bytes.

### `check`

```bash
c4sync check -config apps/usage/c4sync.yml
```

`check` regenerates the files in memory and compares them with the files on disk. It also lists unmapped packages and runs the go-arch-lint rules. It prints nothing when everything is fine.

| Exit code | Meaning |
|---|---|
| 0 | up to date, no violations |
| 1 | unmapped packages or go-arch-lint violations |
| 2 | generated files are stale |
| 3 | stale and violations |
| 4 | the tool could not run (bad config, Go code does not load) |

Here is the output after someone adds an import from `api` to `store` and does not regenerate:

```
stale: golden/components.dsl: regenerate with `c4sync generate`
--- golden/components.dsl (committed)
+++ golden/components.dsl (from code)
@@ -32,4 +32,5 @@
 fx_api -> fx_app "Uses" "Go import" "c4sync"
+fx_api -> fx_store "Uses" "Go import" "c4sync"
 fx_app -> fx_bus "Uses" "Go import" "c4sync"
internal/api/api.go:8: component api must not import example.com/fixture/internal/store
```

`-json` prints the same report as JSON.

### `diff`

```bash
c4sync diff -config head/apps/usage/c4sync.yml -base-config base/apps/usage/c4sync.yml \
  -views changes.dsl -json changes.json
```

`diff` builds the model of two source trees and prints what changed in Markdown. A common setup checks out the base branch with `git worktree add` and compares it with the working tree. Each tree uses its own configs. A missing base config means an empty base, so a new service shows up as fully added.

Here is the real output for HAMSP-26 "Usage Writer stores each event once" (commit `e57aaaf`):

```markdown
## Structure changes: Usage

### writer (important)

- added component: writer (internal/writer)
- added relation: writer → event (`internal/writer/writer.go:17`)
- added relation: writer → store (`internal/writer/writer.go:18`)
- added external relation: writer → s.kafka: Uses (franz-go) (`internal/writer/writer.go:15`)
- added interface: writer consumes topic ? (in NewClient) (`internal/writer/writer.go:48`)
- added code element: struct writer.Writer (`internal/writer/writer.go:27`)
- added code relation: internal/writer.Writer uses internal/store.Writer (`internal/writer/writer.go:29`)

### cmd

- added relation: cmd → writer (`main.go:35`)
- added interface: cmd serves http GET /healthz (`main.go:197`)
```

Changes are grouped by component. A component whose changes touch an `important` component (here `writer → store`) comes first. `-important a,b` adds names to the list from the config.

The flags:

- `-views changes.dsl` writes a standalone workspace with a view of the changed components and their neighbours, plus code views for them. It is written only when something changed.
- `-json changes.json` writes the report as JSON.
- With no changes, `diff` prints nothing and exits 0.

## Render to Mermaid

```bash
java -jar structurizr.war validate -workspace docs/architecture/c4/workspace.dsl
```

```bash
java -jar structurizr.war export -workspace docs/architecture/c4/workspace.dsl -format mermaid -output docs/architecture/c4/mmd
```

The code workspace (`usage.code.dsl`) and the `diff` views file are complete workspaces. Validate and export them the same way.

Structurizr has no code-level element type. c4sync models Go types as components inside a container, grouped by the component they belong to. In Mermaid each type is a box: interfaces have a dashed border, structs a solid one, and the box text lists the methods. You do not get UML class notation.

## What it does not see

- **Values built at runtime.** A topic read from an environment variable, or a route built with `fmt.Sprintf`, shows as `? (in FuncName)`. The diagram tells you where to look. It does not guess the value.
- **SQL is matched with patterns, not parsed.** c4sync finds tables after `FROM`, `JOIN`, `INSERT INTO`, `UPDATE`, `DELETE FROM`, `MERGE INTO`, `COPY`, `CREATE TABLE` and `ALTER TABLE`. A name followed by `(` is a function and shows as `name()`. Postgres built-ins (`pg_*`, `unnest`, `json_*` and others) are skipped. Add your own names to `sql.ignore`.
- **Detectors cover only calls you list.** A library that c4sync has no matcher for adds no routes, topics or tables. Add a `call` or `field` matcher for it.
- **Only Go.** The model does not depend on the language, but there is only a Go extractor.

## Development

```bash
go -C tools/c4sync test ./...
```

The end-to-end tests run the CLI against `testdata/fixture`. It is a small module with an import cycle, a cross-package `implements`, a generic type, an alias, dynamic routes and topics, SQL queries and a migration. Its expected output is in `testdata/fixture/golden/`. After an intended change to the output, rewrite the golden files and review their diff:

```bash
go -C tools/c4sync test -run TestGenerateMatchesGolden -update .
```

The Structurizr test runs only when `STRUCTURIZR_WAR` is set. It validates the fixture workspace, the code workspace and a `diff` views file, and exports them to Mermaid.

```bash
STRUCTURIZR_WAR=~/.local/lib/structurizr.war JAVA="$(/usr/libexec/java_home -v 21+)/bin/java" go -C tools/c4sync test .
```
