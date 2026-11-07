# C4 diagrams generated from the Go code (tools/c4sync/README.md).
#
#   make c4         regenerate docs/architecture/c4/generated/ and the README diagrams
#   make c4-check   what CI runs: stale diagrams, unmapped packages, go-arch-lint rules
#
# c4sync and go-arch-lint are built from tools/c4sync (go-arch-lint is pinned as
# a Go tool there). The README diagrams need Structurizr: see docs/architecture/c4/README.md.

C4_SERVICES := usage exampleapp gateway admin
C4SYNC := $(CURDIR)/bin/c4sync

.PHONY: c4 c4-generate c4-check c4-diff c4sync

c4sync:
	@go -C tools/c4sync build -o $(C4SYNC) .

c4-generate: c4sync
	@export C4SYNC_GO_ARCH_LINT="$$(go -C tools/c4sync tool -n go-arch-lint)"; \
	for s in $(C4_SERVICES); do $(C4SYNC) generate -config apps/$$s/c4sync.yml || exit 1; done

c4: c4-generate
	docs/architecture/c4/render.sh

c4-check: c4sync
	@export C4SYNC_GO_ARCH_LINT="$$(go -C tools/c4sync tool -n go-arch-lint)"; \
	status=0; \
	for s in $(C4_SERVICES); do \
		echo "== apps/$$s"; \
		$(C4SYNC) check -config apps/$$s/c4sync.yml || status=1; \
	done; \
	exit $$status

# Structural changes against another checkout, for example the base branch:
#   git worktree add ../hamsp-base master && make c4-diff BASE=../hamsp-base
# Writes bin/c4-diff/report.md, and a JSON report and a views workspace per
# changed service. report.md is empty when nothing changed.
C4_DIFF_OUT := $(CURDIR)/bin/c4-diff

c4-diff: c4sync
	@test -n "$(BASE)" || { echo "usage: make c4-diff BASE=<checkout of the base>"; exit 2; }
	@export C4SYNC_GO_ARCH_LINT="$$(go -C tools/c4sync tool -n go-arch-lint)"; \
	mkdir -p "$(C4_DIFF_OUT)" && rm -f "$(C4_DIFF_OUT)"/*.views.dsl "$(C4_DIFF_OUT)"/*.json; \
	for s in $(C4_SERVICES); do \
		$(C4SYNC) diff -config apps/$$s/c4sync.yml -base-config $(BASE)/apps/$$s/c4sync.yml \
			-views "$(C4_DIFF_OUT)/$$s.views.dsl" -json "$(C4_DIFF_OUT)/$$s.json" || exit 1; \
	done > "$(C4_DIFF_OUT)/report.md"
