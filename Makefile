.PHONY: build doc-governance fmt fmt-check migration-check mod-verify test test-process test-race verify vet

GO ?= go

fmt:
	$(GO) fmt ./...

fmt-check:
	@files="$$(find cmd internal -type f -name '*.go' -print)"; \
	unformatted="$$(gofmt -l $$files)"; \
	test -z "$$unformatted" || { printf '%s\n' "$$unformatted"; exit 1; }

test:
	$(GO) test ./...

test-process:
	bash scripts/test-backend-processes.sh

test-race:
	$(GO) test -race -shuffle=on ./...

vet:
	$(GO) vet ./...

build:
	$(GO) build ./...

migration-check:
	$(GO) run ./cmd/migration-check -dir db/migrations

mod-verify:
	$(GO) mod verify

doc-governance:
	bash scripts/check-doc-governance.sh

verify: fmt-check test test-process vet build migration-check mod-verify doc-governance
