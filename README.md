# Notifier

Notification-Microservice in Go. Andere Services senden Aufträge per REST-API, der Notifier rendert Templates und verschickt sie über konfigurierbare Channels (vorerst E-Mail via SMTP).

**Status:** Phase 0 und 1 (Fundament, Config, Domain, Stores). Die Health-Endpunkte laufen. Templates, E-Mail-Versand, Dispatcher und die eigentliche REST-API folgen in den nächsten Phasen.

## Schnellstart

```bash
cp .env.example .env          # PSK und PUBLIC_BASE_URL anpassen
make network                  # einmalig: Docker-Netzwerk anlegen, falls nötig
make dev                      # Notifier + Mailpit (Mail-UI: http://localhost:8025)

curl localhost:8080/healthz   # {"status":"ok"}
make logs
```

Ohne Docker: `make run` (liest `./.env`, braucht Go 1.23+).

## Konfiguration

| Wo | Was |
|---|---|
| `.env` | Ports, PSKs, Logging, Queue, Retry, Pfade. Alle Variablen sind in `.env.example` dokumentiert. Echte Umgebungsvariablen haben Vorrang. |
| `config/channels/<channel>.yaml` | Eine Datei pro Channel. Der Dateiname ist der Channel-Name. Werte können mit `${VAR}` oder `${VAR:-default}` auf die `.env` verweisen. Dateien mit `_`-Präfix sind deaktiviert. |

Fehlerhafte Konfiguration bricht den Start ab. Es werden **alle** Probleme auf einmal gemeldet:

```
notifier: invalid configuration (2 problems):
  - API_PSKS: entry 1 (orders): secret is too short (4 characters, minimum 16)
  - WORKER_COUNT: 0 is out of range (allowed: 1-64)
```

### Ports

| Port | Zweck | Erreichbar für |
|---|---|---|
| `API_PORT` (8080) | REST-API, PSK-geschützt (ab Phase 5) | andere Services im Docker-Netzwerk |
| `PUBLIC_PORT` (8081) | nur `/confirm/{token}` (ab Phase 6) | Reverse Proxy leitet nur diesen Pfad hierher |

## Projektstruktur

```
cmd/notifier/        main: Wiring, Graceful Shutdown, `healthcheck`-Subcommand
internal/config/     .env-Loader, Validierung, Channel-YAML-Loader mit ${ENV}-Expansion
internal/domain/     Notification, Status-Übergänge, SendError (temporär/permanent), Sentinel-Fehler
internal/store/      Repository-Interfaces; memory/ = In-Memory-Implementierung
internal/api/        HTTP-Server (aktuell: /healthz, /readyz)
templates/           Mail-Templates (ab Phase 2)
config/channels/     eine YAML pro Channel
```

## Entwicklung

```bash
make test-race   # Tests mit Race-Detector
make lint        # go vet (+ golangci-lint, falls installiert)
make cover       # Abdeckung
```

## Entwurfsentscheidungen

- **Persistenz nur über Interfaces** (`internal/store`). Der Adapter für den zentralen DB-Service kommt später neben `memory/`, ohne Änderungen am Rest.
- **Bestätigungs-Tokens werden nur als Hash gespeichert**, das Einlösen ist atomar und einmalig.
- **Nicht klassifizierte Sendefehler gelten als permanent.** Lieber eine Mail als fehlgeschlagen melden, als sie durch blindes Wiederholen doppelt zuzustellen.
- **Channel-Configs werden streng dekodiert.** Tippfehler wie `hots:` statt `host:` fallen beim Start auf.
- **Secrets landen nie in Logs oder Fehlermeldungen.** PSKs werden nur mit Namen geloggt.
