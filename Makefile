.PHONY: up down test test-integration web lint demo chaos load
up:            ## build and start the full stack
	docker compose up -d --build
down:
	docker compose down -v
test:          ## unit + use-case + in-process e2e (embedded NATS)
	go test -race ./...
test-integration: ## Postgres adapters via testcontainers
	go test -race -tags integration ./test/integration/...
web:
	cd web && npm ci && npm run build && npm test
lint:
	golangci-lint run
demo:
	scripts/place-order.sh alice
chaos:         ## make chaos SCENARIO=payments-decline
	scripts/chaos.sh $(SCENARIO)
load:
	docker run --rm -i --network host grafana/k6 run - < loadtest/k6.js
