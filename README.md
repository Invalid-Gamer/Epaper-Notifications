# Epaper-Notifications

Notification-Microservice in Go. Andere Services senden Aufträge per REST-API, der Service rendert Templates und verschickt sie über konfigurierbare Channels (vorerst E-Mail via SMTP).

**Status:** Phase 2 von 7 abgeschlossen (Fundament, Config, Domain, Stores, **Template-Engine**). Health-Endpunkte und Template-Werkzeuge laufen. E-Mail-Versand, Dispatcher und die eigentliche REST-API folgen. Den genauen Stand, Ziele und nächste Schritte zeigt [docs/PROGRESS.md](docs/PROGRESS.md).

## Schnellstart

```bash
cp .env.example .env          # PSK und PUBLIC_BASE_URL anpassen
make network                  # einmalig: Docker-Netzwerk anlegen, falls nötig
make dev                      # Epaper-Notifications + Mailpit (Mail-UI: http://localhost:8025)

curl localhost:8080/healthz   # {"status":"ok"}
make logs
```

Ohne Docker: `make run` (liest `./.env`, braucht Go 1.27+).

## Konfiguration

| Wo | Was |
|---|---|
| `.env` | Ports, PSKs, Logging, Queue, Retry, Pfade. Alle Variablen sind in `.env.example` dokumentiert. Echte Umgebungsvariablen haben Vorrang. |
| `config/channels/<channel>.yaml` | Eine Datei pro Channel. Der Dateiname ist der Channel-Name. Werte können mit `${VAR}` oder `${VAR:-default}` auf die `.env` verweisen. Dateien mit `_`-Präfix sind deaktiviert. |
| `templates/<name>.tmpl` | Eine Datei pro Template, HTML oder Text. Siehe [docs/TEMPLATES.md](templates/TEMPLATES.md). |

Fehlerhafte Konfiguration oder fehlerhafte Templates brechen den Start ab. Es werden **alle** Probleme auf einmal gemeldet:

```
epaper-notifications: invalid configuration (2 problems):
  - API_PSKS: entry 1 (orders): secret is too short (4 characters, minimum 16)
  - WORKER_COUNT: 0 is out of range (allowed: 1-64)
```

### Ports

| Port | Zweck | Erreichbar für |
|---|---|---|
| `API_PORT` (8080) | REST-API, PSK-geschützt (ab Phase 5) | andere Services im Docker-Netzwerk |
| `PUBLIC_PORT` (8081) | nur `/confirm/{token}` (ab Phase 6) | Reverse Proxy leitet nur diesen Pfad hierher |

## Templates

Ein neues Template ist eine neue Datei in `templates/`:

```bash
make templates-check                                   # alle Templates prüfen
make template-render T=alert D='{"title":"Disk voll","message":"Nur noch 2% frei"}'
make restart                                           # Templates werden beim Start geladen
```

Mitgeliefert: `password_reset`, `verify_email`, `support` (HTML) sowie `two_factor`, `alert` (Text).

## Projektstruktur

```
cmd/epaper-notifications/   main: Wiring, Graceful Shutdown, Unterbefehle (healthcheck, templates)
internal/config/            .env-Loader, Validierung, Channel-YAML-Loader mit ${ENV}-Expansion
internal/domain/            Notification, Status-Übergänge, SendError (temporär/permanent), Format
internal/store/             Repository-Interfaces; memory/ = In-Memory-Implementierung
internal/template/          Template-Engine: Laden, Validieren, Rendern (HTML/Text)
internal/api/               HTTP-Server (aktuell: /healthz, /readyz)
templates/                  Mail-Templates
config/channels/            eine YAML pro Channel
docs/                       Fortschritt (PROGRESS.md) und Template-Anleitung (TEMPLATES.md)
test/                       alle Tests (externe Tests, package test)
```

## Entwicklung

```bash
make test-race   # Tests mit Race-Detector
make lint        # go vet (+ golangci-lint, falls installiert)
make cover       # Abdeckung über internal/...
```

## Entwurfsentscheidungen

- **Persistenz nur über Interfaces** (`internal/store`). Der Adapter für den zentralen DB-Service kommt später neben `memory/`, ohne Änderungen am Rest.
- **Bestätigungs-Tokens werden nur als Hash gespeichert**, das Einlösen ist atomar und einmalig.
- **Nicht klassifizierte Sendefehler gelten als permanent.** Lieber eine Mail als fehlgeschlagen melden, als sie durch blindes Wiederholen doppelt zuzustellen.
- **Config und Templates werden streng geprüft.** Tippfehler wie `hots:` statt `host:` oder `{{.usr_name}}` statt `{{.user_name}}` fallen beim Start auf, nicht beim Versand.
- **Secrets landen nie in Logs oder Fehlermeldungen.** PSKs werden nur mit Namen geloggt.
