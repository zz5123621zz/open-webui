.PHONY: check test test-race web-check web-build e2e compose-check

check: test web-check web-build compose-check

test:
	go test ./...
	go vet ./...

test-race:
	go test -race ./...

web-check:
	npm --prefix web ci --no-audit --no-fund
	npm --prefix web audit --audit-level=moderate
	npm --prefix web run check

web-build:
	npm --prefix web run build

e2e:
	npm --prefix web run test:e2e

compose-check:
	@test -n "$(APP_IMAGE)" || (echo "APP_IMAGE is required" >&2; exit 1)
	@test -n "$(AI_DEFAULT_MODEL)" || (echo "AI_DEFAULT_MODEL is required" >&2; exit 1)
	docker compose -f compose.yaml config --quiet
