package test

import (
	"epaper-notificaitons/internal/config"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

const goodPSK = "0123456789abcdef0123" // 20 Zeichen

// env baut eine Lookup-Funktion aus einer Map und füllt Pflichtwerte auf.
func env(t *testing.T, overrides map[string]string) config.Lookup {
	t.Helper()
	dir := t.TempDir()
	m := map[string]string{
		"PUBLIC_BASE_URL": "https://notify.example.com",
		"API_PSKS":        "orders:" + goodPSK,
		"TEMPLATES_DIR":   dir,
		"CHANNELS_DIR":    dir,
	}
	for k, v := range overrides {
		m[k] = v
	}
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func problems(t *testing.T, err error) []string {
	t.Helper()
	var ce *config.Error
	if !errors.As(err, &ce) {
		t.Fatalf("expected *config.Error, got %T: %v", err, err)
	}
	return ce.Problems
}

func hasProblem(ps []string, substr string) bool {
	for _, p := range ps {
		if strings.Contains(p, substr) {
			return true
		}
	}
	return false
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := config.Load(env(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIPort != 8080 || cfg.PublicPort != 8081 {
		t.Errorf("ports: %d/%d", cfg.APIPort, cfg.PublicPort)
	}
	if cfg.LogLevel != slog.LevelInfo || cfg.LogFormat != "json" {
		t.Errorf("log: %v %s", cfg.LogLevel, cfg.LogFormat)
	}
	if cfg.WorkerCount != 4 || cfg.QueueSize != 1000 || cfg.RetryMaxAttempts != 5 {
		t.Errorf("dispatcher defaults: %+v", cfg)
	}
	if cfg.RetryBaseDelay != 5*time.Second || cfg.ConfirmTokenTTL != 24*time.Hour {
		t.Errorf("durations: %v %v", cfg.RetryBaseDelay, cfg.ConfirmTokenTTL)
	}
	if len(cfg.APIKeys) != 1 || cfg.APIKeys[0].Name != "orders" || cfg.APIKeys[0].Secret != goodPSK {
		t.Errorf("api keys: %v", cfg.APIKeys)
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := config.Load(env(t, map[string]string{
		"API_PORT":           "9000",
		"PUBLIC_PORT":        "9001",
		"PUBLIC_BASE_URL":    "http://localhost:9001/",
		"LOG_LEVEL":          "DEBUG",
		"LOG_FORMAT":         "text",
		"WORKER_COUNT":       "8",
		"RETRY_BASE_DELAY":   "250ms",
		"RETRY_MAX_DELAY":    "1m",
		"CONFIRM_TOKEN_TTL":  "48h",
		"RETRY_MAX_ATTEMPTS": " 3 ",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIPort != 9000 || cfg.PublicPort != 9001 {
		t.Errorf("ports: %d/%d", cfg.APIPort, cfg.PublicPort)
	}
	if cfg.PublicBaseURL != "http://localhost:9001" {
		t.Errorf("trailing slash not trimmed: %q", cfg.PublicBaseURL)
	}
	if cfg.LogLevel != slog.LevelDebug || cfg.LogFormat != "text" {
		t.Errorf("log: %v %s", cfg.LogLevel, cfg.LogFormat)
	}
	if cfg.WorkerCount != 8 || cfg.RetryMaxAttempts != 3 {
		t.Errorf("ints: %d %d", cfg.WorkerCount, cfg.RetryMaxAttempts)
	}
	if cfg.RetryBaseDelay != 250*time.Millisecond || cfg.ConfirmTokenTTL != 48*time.Hour {
		t.Errorf("durations: %v %v", cfg.RetryBaseDelay, cfg.ConfirmTokenTTL)
	}
}

func TestLoadCollectsAllProblems(t *testing.T) {
	_, err := config.Load(env(t, map[string]string{
		"API_PORT":          "abc",
		"PUBLIC_PORT":       "70000",
		"PUBLIC_BASE_URL":   "ftp://x",
		"API_PSKS":          "",
		"LOG_LEVEL":         "loud",
		"WORKER_COUNT":      "0",
		"RETRY_BASE_DELAY":  "soon",
		"CONFIRM_TOKEN_TTL": "1s",
		"TEMPLATES_DIR":     "/definitely/not/here",
	}))
	ps := problems(t, err)
	for _, want := range []string{
		"API_PORT", "PUBLIC_PORT", "PUBLIC_BASE_URL", "API_PSKS",
		"LOG_LEVEL", "WORKER_COUNT", "RETRY_BASE_DELAY", "CONFIRM_TOKEN_TTL", "TEMPLATES_DIR",
	} {
		if !hasProblem(ps, want+":") {
			t.Errorf("missing problem for %s in %v", want, ps)
		}
	}
	if len(ps) != 9 {
		t.Errorf("expected 9 problems, got %d: %v", len(ps), ps)
	}
	// Die Fehlermeldung ist lesbar und nennt die Anzahl.
	if !strings.Contains(err.Error(), "9 problems") {
		t.Errorf("message: %s", err)
	}
}

func TestRequiredMissing(t *testing.T) {
	lookup := func(k string) (string, bool) { return "", false }
	_, err := config.Load(lookup)
	ps := problems(t, err)
	if !hasProblem(ps, "PUBLIC_BASE_URL: is required") || !hasProblem(ps, "API_PSKS: is required") {
		t.Errorf("problems: %v", ps)
	}
}

func TestCrossChecks(t *testing.T) {
	_, err := config.Load(env(t, map[string]string{"API_PORT": "8080", "PUBLIC_PORT": "8080"}))
	if !hasProblem(problems(t, err), "must differ") {
		t.Errorf("same ports not rejected: %v", err)
	}

	_, err = config.Load(env(t, map[string]string{"RETRY_BASE_DELAY": "1m", "RETRY_MAX_DELAY": "10s"}))
	if !hasProblem(problems(t, err), "RETRY_MAX_DELAY") {
		t.Errorf("max<base not rejected: %v", err)
	}
}

func TestAPIKeyParsing(t *testing.T) {
	long := func(c string) string { return strings.Repeat(c, 20) }

	tests := []struct {
		name    string
		raw     string
		want    []string // erwartete Namen
		problem string   // Teilstring eines erwarteten Problems, leer = kein Fehler
	}{
		{"single", "orders:" + long("a"), []string{"orders"}, ""},
		{"multiple with spaces", " orders:" + long("a") + " , auth:" + long("b"), []string{"orders", "auth"}, ""},
		{"colon in secret", "orders:" + long("a") + ":rest", []string{"orders"}, ""},
		{"trailing comma", "orders:" + long("a") + ",", []string{"orders"}, ""},
		{"missing name", ":" + long("a"), nil, "expected format name:secret"},
		{"missing colon", "justasecretwithoutname", nil, "expected format name:secret"},
		{"empty secret", "orders:", nil, "expected format name:secret"},
		{"short secret", "orders:short", nil, "too short"},
		{"bad name", "or ders:" + long("a"), nil, "may only contain"},
		{"duplicate name", "a:" + long("x") + ",a:" + long("y"), []string{"a"}, "used twice"},
		{"duplicate secret", "a:" + long("x") + ",b:" + long("x"), []string{"a"}, "identical"},
		{"only commas", " , ,", nil, "contains no valid entry"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := config.Load(env(t, map[string]string{"API_PSKS": tt.raw}))
			if tt.problem == "" {
				if err != nil {
					t.Fatal(err)
				}
				var names []string
				for _, k := range cfg.APIKeys {
					names = append(names, k.Name)
				}
				if fmt.Sprint(names) != fmt.Sprint(tt.want) {
					t.Fatalf("names = %v, want %v", names, tt.want)
				}
				return
			}
			ps := problems(t, err)
			if !hasProblem(ps, tt.problem) {
				t.Fatalf("problems %v do not contain %q", ps, tt.problem)
			}
		})
	}
}

func TestSecretsNeverLeak(t *testing.T) {
	secret := "SUPERSECRETSUPERSECRET"
	cfg, err := config.Load(env(t, map[string]string{"API_PSKS": "orders:" + secret}))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		fmt.Sprintf("%v", cfg.APIKeys),
		fmt.Sprintf("%+v", cfg.APIKeys[0]),
		fmt.Sprintf("%#v", cfg.APIKeys[0]),
		cfg.LogValue().String(),
	} {
		if strings.Contains(s, secret) {
			t.Errorf("secret leaked in %q", s)
		}
	}

	// Auch in Fehlermeldungen: zu kurzes Secret mit erkennbarem Inhalt.
	_, err = config.Load(env(t, map[string]string{"API_PSKS": "orders:LEAKME", "API_PORT": "x"}))
	if err == nil || strings.Contains(err.Error(), "LEAKME") {
		t.Errorf("secret in error message: %v", err)
	}
}

func TestDirMustBeDirectory(t *testing.T) {
	file := t.TempDir() + "/file.txt"
	if err := writeFile(file, "x"); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(env(t, map[string]string{"CHANNELS_DIR": file}))
	if !hasProblem(problems(t, err), "not a directory") {
		t.Errorf("got %v", err)
	}
}
