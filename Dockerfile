# syntax=docker/dockerfile:1

# ---------- Build ----------
FROM golang:1.23-alpine AS build
WORKDIR /src

# Abhängigkeiten zuerst, damit der Layer-Cache greift, solange sich go.mod/go.sum nicht ändern.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/epaper-notifications ./cmd/epaper-notifications

# ---------- Runtime ----------
# distroless/static: keine Shell, kein Paketmanager, aber CA-Zertifikate (für TLS zum Mailserver).
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app

COPY --from=build /out/epaper-notifications /app/epaper-notifications
# Standard-Templates und -Channels sind im Image. Per Volume (siehe docker-compose.yml)
# lassen sie sich ohne Rebuild austauschen.
COPY templates /app/templates
COPY config /app/config

USER nonroot:nonroot
EXPOSE 8080 8081

# Das Binary prüft sich selbst (kein curl im Image).
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
  CMD ["/app/epaper-notifications", "healthcheck"]

ENTRYPOINT ["/app/epaper-notifications"]
