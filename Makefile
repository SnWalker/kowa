.PHONY: build contract-check doc-governance fmt fmt-check migration-check mod-verify test test-identity-workspace-integration test-knowledge-artifact-integration test-process test-race test-workflow-execution-integration verify vet

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

test-identity-workspace-integration:
	bash scripts/test-identity-workspace-integration.sh

test-workflow-execution-integration:
	bash scripts/test-workflow-execution-integration.sh

test-knowledge-artifact-integration:
	bash scripts/test-knowledge-artifact-integration.sh

test-race:
	$(GO) test -race -shuffle=on ./...

vet:
	$(GO) vet ./...

build:
	$(GO) build ./...

migration-check:
	$(GO) run ./cmd/migration-check -dir db/migrations

contract-check:
	bash scripts/check-identity-workspace-contract.sh
	bash scripts/check-workflow-execution-contract.sh
	bash scripts/check-knowledge-artifact-contract.sh

mod-verify:
	$(GO) mod verify

doc-governance:
	bash scripts/check-doc-governance-test.sh

verify: fmt-check test test-process vet build migration-check contract-check mod-verify doc-governance
