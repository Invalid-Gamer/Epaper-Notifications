// Package config lädt und validiert die Konfiguration des Servicess:
// die .env bzw. Umgebungsvariablen (config.go, dotenv.go) und die Channel-Dateien
// unter config/channels/ (channels.go).
package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Lookup entspricht os.LookupEnv. Als Funktion injiziert, damit Tests ohne
// echte Umgebungsvariablen auskommen.
type Lookup func(key string) (string, bool)

// APIKey ist ein benannter Pre-Shared-Key eines aufrufenden Services.
// Der Name taucht in den Logs auf, das Secret nie.
type APIKey struct {
	Name   string
	Secret string
}

func (k APIKey) String() string       { return "APIKey(" + k.Name + ")" }
func (k APIKey) GoString() string     { return k.String() }
func (k APIKey) LogValue() slog.Value { return slog.StringValue(k.Name) }

// Config ist die vollständige, validierte Laufzeitkonfiguration.
type Config struct {
	APIPort          int
	PublicPort       int
	PublicBaseURL    string // ohne abschließenden Slash
	APIKeys          []APIKey
	LogLevel         slog.Level
	LogFormat        string // "json" | "text"
	TemplatesDir     string
	ChannelsDir      string
	WorkerCount      int
	QueueSize        int
	RetryMaxAttempts int
	RetryBaseDelay   time.Duration
	RetryMaxDelay    time.Duration
	ConfirmTokenTTL  time.Duration
	ShutdownTimeout  time.Duration
	MaxBodyBytes     int64
}

// LogValue sorgt dafür, dass die Config gefahrlos geloggt werden kann (ohne Secrets).
func (c *Config) LogValue() slog.Value {
	names := make([]string, len(c.APIKeys))
	for i, k := range c.APIKeys {
		names[i] = k.Name
	}
	return slog.GroupValue(
		slog.Int("api_port", c.APIPort),
		slog.Int("public_port", c.PublicPort),
		slog.String("public_base_url", c.PublicBaseURL),
		slog.Any("api_clients", names),
		slog.String("templates_dir", c.TemplatesDir),
		slog.String("channels_dir", c.ChannelsDir),
		slog.Int("workers", c.WorkerCount),
		slog.Int("queue_size", c.QueueSize),
		slog.Int("retry_max_attempts", c.RetryMaxAttempts),
		slog.String("retry_base_delay", c.RetryBaseDelay.String()),
		slog.String("retry_max_delay", c.RetryMaxDelay.String()),
		slog.String("confirm_token_ttl", c.ConfirmTokenTTL.String()),
	)
}

// Error sammelt alle Probleme der Konfiguration, damit man beim Start nicht
// einen Fehler nach dem anderen beheben muss.
type Error struct {
	Problems []string
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "invalid configuration (%d problem", len(e.Problems))
	if len(e.Problems) != 1 {
		b.WriteString("s")
	}
	b.WriteString("):")
	for _, p := range e.Problems {
		b.WriteString("\n  - ")
		b.WriteString(p)
	}
	return b.String()
}

// FromEnv lädt die Konfiguration aus der Prozessumgebung.
func FromEnv() (*Config, error) { return Load(os.LookupEnv) }

// Load liest und validiert alle Werte. Bei Fehlern kommt ein *Error mit
// sämtlichen gefundenen Problemen zurück.
func Load(lookup Lookup) (*Config, error) {
	r := &reader{lookup: lookup}

	cfg := &Config{
		APIPort:          r.port("API_PORT", 8080),
		PublicPort:       r.port("PUBLIC_PORT", 8081),
		PublicBaseURL:    r.baseURL("PUBLIC_BASE_URL"),
		APIKeys:          r.apiKeys("API_PSKS"),
		LogLevel:         r.logLevel("LOG_LEVEL", slog.LevelInfo),
		LogFormat:        r.oneOf("LOG_FORMAT", "json", "json", "text"),
		TemplatesDir:     r.dir("TEMPLATES_DIR", "./templates"),
		ChannelsDir:      r.dir("CHANNELS_DIR", "./config/channels"),
		WorkerCount:      r.intRange("WORKER_COUNT", 4, 1, 64),
		QueueSize:        r.intRange("QUEUE_SIZE", 1000, 1, 1_000_000),
		RetryMaxAttempts: r.intRange("RETRY_MAX_ATTEMPTS", 5, 1, 20),
		RetryBaseDelay:   r.duration("RETRY_BASE_DELAY", 5*time.Second, 100*time.Millisecond),
		RetryMaxDelay:    r.duration("RETRY_MAX_DELAY", 10*time.Minute, time.Second),
		ConfirmTokenTTL:  r.duration("CONFIRM_TOKEN_TTL", 24*time.Hour, time.Minute),
		ShutdownTimeout:  r.duration("SHUTDOWN_TIMEOUT", 30*time.Second, time.Second),
		MaxBodyBytes:     int64(r.intRange("MAX_BODY_BYTES", 1<<20, 1024, 64<<20)),
	}

	// Querprüfungen, nur sinnvoll, wenn die Einzelwerte gültig waren.
	if len(r.problems) == 0 {
		if cfg.APIPort == cfg.PublicPort {
			r.add("API_PORT/PUBLIC_PORT", "must differ (both are %d); the public port must be a separate listener", cfg.APIPort)
		}
		if cfg.RetryMaxDelay < cfg.RetryBaseDelay {
			r.add("RETRY_MAX_DELAY", "must be >= RETRY_BASE_DELAY (%s)", cfg.RetryBaseDelay)
		}
	}

	if len(r.problems) > 0 {
		return nil, &Error{Problems: r.problems}
	}
	return cfg, nil
}

// ------------------------------------------------------------------- reader

type reader struct {
	lookup   Lookup
	problems []string
}

func (r *reader) add(key, format string, args ...any) {
	r.problems = append(r.problems, key+": "+fmt.Sprintf(format, args...))
}

// get liefert den getrimmten Wert; ok ist false, wenn nicht gesetzt oder leer.
func (r *reader) get(key string) (string, bool) {
	v, set := r.lookup(key)
	v = strings.TrimSpace(v)
	return v, set && v != ""
}

func (r *reader) port(key string, def int) int {
	return r.intRange(key, def, 1, 65535)
}

func (r *reader) intRange(key string, def, min, max int) int {
	v, ok := r.get(key)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		r.add(key, "%q is not a valid integer", v)
		return def
	}
	if n < min || n > max {
		r.add(key, "%d is out of range (allowed: %d-%d)", n, min, max)
		return def
	}
	return n
}

func (r *reader) duration(key string, def, min time.Duration) time.Duration {
	v, ok := r.get(key)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		r.add(key, "%q is not a valid duration (examples: 500ms, 5s, 10m, 24h)", v)
		return def
	}
	if d < min {
		r.add(key, "%s is too small (minimum: %s)", d, min)
		return def
	}
	return d
}

func (r *reader) oneOf(key, def string, allowed ...string) string {
	v, ok := r.get(key)
	if !ok {
		return def
	}
	v = strings.ToLower(v)
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	r.add(key, "%q is not allowed (allowed: %s)", v, strings.Join(allowed, ", "))
	return def
}

func (r *reader) logLevel(key string, def slog.Level) slog.Level {
	v, ok := r.get(key)
	if !ok {
		return def
	}
	var l slog.Level
	// slog akzeptiert "debug", "info", "warn", "error" (auch mit Offset, das erlauben wir nicht).
	switch strings.ToLower(v) {
	case "debug":
		l = slog.LevelDebug
	case "info":
		l = slog.LevelInfo
	case "warn", "warning":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		r.add(key, "%q is not allowed (allowed: debug, info, warn, error)", v)
		return def
	}
	return l
}

func (r *reader) dir(key, def string) string {
	v, ok := r.get(key)
	if !ok {
		v = def
	}
	st, err := os.Stat(v)
	switch {
	case err != nil:
		r.add(key, "directory %q is not accessible: %v", v, unwrapPathError(err))
	case !st.IsDir():
		r.add(key, "%q is not a directory", v)
	}
	return v
}

func (r *reader) baseURL(key string) string {
	v, ok := r.get(key)
	if !ok {
		r.add(key, "is required (externally reachable URL of the public port, e.g. https://notify.example.com)")
		return ""
	}
	u, err := url.Parse(v)
	switch {
	case err != nil:
		r.add(key, "%q is not a valid URL: %v", v, err)
	case u.Scheme != "http" && u.Scheme != "https":
		r.add(key, "%q must start with http:// or https://", v)
	case u.Host == "":
		r.add(key, "%q has no host", v)
	case u.RawQuery != "" || u.Fragment != "":
		r.add(key, "%q must not contain a query or fragment", v)
	}
	return strings.TrimRight(v, "/")
}

var keyNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

const minPSKLength = 16

// apiKeys parst "name:secret,name2:secret2". Secrets dürfen Doppelpunkte enthalten,
// aber keine Kommas. Aus Fehlermeldungen bleiben Secrets bewusst heraus.
func (r *reader) apiKeys(key string) []APIKey {
	v, ok := r.get(key)
	if !ok {
		r.add(key, "is required (format: name:secret[,name:secret...], secret at least %d characters, e.g. generated with: openssl rand -hex 32)", minPSKLength)
		return nil
	}

	var (
		keys        []APIKey
		seenNames   = map[string]bool{}
		seenSecrets = map[string]bool{}
	)
	for i, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		pos := i + 1
		name, secret, ok := strings.Cut(part, ":")
		name, secret = strings.TrimSpace(name), strings.TrimSpace(secret)
		switch {
		case !ok || name == "" || secret == "":
			r.add(key, "entry %d: expected format name:secret", pos)
			continue
		case !keyNameRe.MatchString(name):
			r.add(key, "entry %d: name %q may only contain letters, digits, '_', '.', '-'", pos, name)
			continue
		case len(secret) < minPSKLength:
			r.add(key, "entry %d (%s): secret is too short (%d characters, minimum %d)", pos, name, len(secret), minPSKLength)
			continue
		case seenNames[name]:
			r.add(key, "entry %d: name %q is used twice", pos, name)
			continue
		case seenSecrets[secret]:
			r.add(key, "entry %d (%s): secret is identical to another entry; every service needs its own", pos, name)
			continue
		}
		seenNames[name], seenSecrets[secret] = true, true
		keys = append(keys, APIKey{Name: name, Secret: secret})
	}
	if len(keys) == 0 && len(r.problems) == 0 {
		r.add(key, "contains no valid entry")
	}
	return keys
}

// unwrapPathError kürzt "stat /x: no such file or directory" auf den Kern.
func unwrapPathError(err error) error {
	if pe, ok := err.(*os.PathError); ok {
		return pe.Err
	}
	return err
}
