VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMPOSE ?= docker compose

.DEFAULT_GOAL := help
.PHONY: help build run test test-race cover lint fmt tidy templates-check template-render network up dev down restart logs ps clean

help: ## Diese Hilfe anzeigen
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

# ---------------------------------------------------------------- Go
build: ## Binary nach ./bin/epaper-notifications bauen
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o bin/epaper-notifications ./cmd/epaper-notifications

run: ## Lokal starten (liest ./.env)
	go run ./cmd/epaper-notifications

test: ## Tests ausführen
	go test ./...

test-race: ## Tests mit Race-Detector
	go test -race -count=1 ./...

cover: ## Test-Abdeckung
	go test -coverpkg=./internal/... -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -n 1

lint: ## go vet (+ golangci-lint, falls installiert)
	go vet ./...
	@command -v golangci-lint >/dev/null && golangci-lint run || echo "golangci-lint nicht installiert, übersprungen"

fmt: ## Code formatieren
	gofmt -l -w .

tidy: ## go.mod/go.sum aufräumen
	go mod tidy

templates-check: ## Alle Templates prüfen (Header, Syntax, Variablen, Escaping)
	go run ./cmd/epaper-notifications templates check

template-render: ## Template rendern: make template-render T=alert D='{"title":"x","message":"y"}'
	@test -n "$(T)" || { echo 'Aufruf: make template-render T=<name> D='"'"'{"var":"wert"}'"'"; exit 2; }
	go run ./cmd/epaper-notifications templates render $(T) '$(D)'

# ---------------------------------------------------------------- Docker
network: ## Externes Docker-Netzwerk anlegen, falls es fehlt
	@net=$$(grep -E '^DOCKER_NETWORK=' .env 2>/dev/null | cut -d= -f2); net=$${net:-platform}; \
	docker network inspect $$net >/dev/null 2>&1 && echo "Netzwerk '$$net' existiert bereits" || docker network create $$net

up: ## Notifier im Hintergrund starten
	VERSION=$(VERSION) $(COMPOSE) up -d --build

dev: ## Notifier + Mailpit starten
	VERSION=$(VERSION) $(COMPOSE) --profile dev up -d --build

down: ## Alles stoppen (auch Mailpit)
	$(COMPOSE) --profile dev down

restart: ## Notifier neu starten (z. B. nach Template-/Config-Änderung)
	$(COMPOSE) restart epaper-notifications

logs: ## Logs verfolgen
	$(COMPOSE) logs -f epaper-notifications

ps: ## Container-Status inkl. Healthcheck
	$(COMPOSE) --profile dev ps

clean: ## Build-Artefakte löschen
	rm -rf bin coverage.out
