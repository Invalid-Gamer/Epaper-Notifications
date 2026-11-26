package test

import (
	"epaper-notificaitons/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDotEnv(t *testing.T) {
	input := "\ufeff# Kommentar\n" +
		"\n" +
		"PLAIN=value\n" +
		"  SPACED = padded value  \n" +
		"export EXPORTED=yes\n" +
		"INLINE=abc # kommentar\n" +
		"HASH_KEPT=pass#word\n" +
		"DQ=\"with spaces # not a comment\"\n" +
		"ESC=\"line1\\nline2 \\\"q\\\" \\\\ \\$HOME\"\n" +
		"SQ='literal \\n $HOME'\n" +
		"AFTER_QUOTE=\"x\" # c\n" +
		"EMPTY=\n" +
		"URL=https://a.b/c?d=e&f=g\n"

	got, err := config.ParseDotEnv(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"PLAIN":       "value",
		"SPACED":      "padded value",
		"EXPORTED":    "yes",
		"INLINE":      "abc",
		"HASH_KEPT":   "pass#word",
		"DQ":          "with spaces # not a comment",
		"ESC":         "line1\nline2 \"q\" \\ $HOME",
		"SQ":          `literal \n $HOME`,
		"AFTER_QUOTE": "x",
		"EMPTY":       "",
		"URL":         "https://a.b/c?d=e&f=g",
	}
	for k, w := range want {
		if g, ok := got[k]; !ok || g != w {
			t.Errorf("%s = %q (present=%v), want %q", k, g, ok, w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d keys, want %d: %v", len(got), len(want), got)
	}
}

func TestParseDotEnvErrors(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{"no equals", "JUSTKEY\n", "line 1: expected KEY=VALUE"},
		{"bad key", "OK=1\n1BAD=x\n", "line 2: invalid variable name"},
		{"space in key", "A B=x\n", "invalid variable name"},
		{"unterminated dq", "A=\"abc\n", "unterminated double quote"},
		{"unterminated sq", "A='abc\n", "unterminated single quote"},
		{"junk after quote", "A=\"abc\" junk\n", "unexpected text after closing quote"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.ParseDotEnv(strings.NewReader(tt.input))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestLoadDotEnvPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("NOTIFICATIONS_T_FROM_FILE=file\nNOTIFICATIONS_T_OVERRIDDEN=file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOTIFICATIONS_T_OVERRIDDEN", "process") // t.Setenv räumt nach dem Test auf
	os.Unsetenv("NOTIFICATIONS_T_FROM_FILE")
	t.Cleanup(func() { os.Unsetenv("NOTIFICATIONS_T_FROM_FILE") })

	loaded, err := config.LoadDotEnv(path)
	if err != nil || !loaded {
		t.Fatalf("loaded=%v err=%v", loaded, err)
	}
	if got := os.Getenv("NOTIFICATIONS_T_FROM_FILE"); got != "file" {
		t.Errorf("FROM_FILE = %q", got)
	}
	if got := os.Getenv("NOTIFICATIONS_T_OVERRIDDEN"); got != "process" {
		t.Errorf("process env must win, got %q", got)
	}
}

func TestLoadDotEnvMissingFileIsNoError(t *testing.T) {
	loaded, err := config.LoadDotEnv(filepath.Join(t.TempDir(), "nope.env"))
	if err != nil || loaded {
		t.Fatalf("loaded=%v err=%v", loaded, err)
	}
}

func TestLoadDotEnvSyntaxErrorIsReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	_ = os.WriteFile(path, []byte("BROKEN\n"), 0o600)
	if _, err := config.LoadDotEnv(path); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v", err)
	}
}
