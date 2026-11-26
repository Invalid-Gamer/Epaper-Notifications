package config

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strings"
)

var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// LoadDotEnv liest eine .env-Datei und setzt alle Variablen, die in der
// Prozessumgebung noch nicht gesetzt sind. Echte Umgebungsvariablen (z. B. aus
// Docker oder Compose) haben also immer Vorrang vor der Datei.
//
// Fehlt die Datei, ist das kein Fehler (loaded == false). Im Container kommen
// die Variablen meist per env_file und es gibt keine .env im Image.
func LoadDotEnv(path string) (loaded bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("open env file %q: %w", path, err)
	}
	defer f.Close()

	vars, err := ParseDotEnv(f)
	if err != nil {
		return false, fmt.Errorf("parse env file %q: %w", path, err)
	}
	for k, v := range vars {
		if _, set := os.LookupEnv(k); set {
			continue
		}
		if err := os.Setenv(k, v); err != nil {
			return false, fmt.Errorf("set %s: %w", k, err)
		}
	}
	return true, nil
}

// ParseDotEnv parst das übliche .env-Format:
//
//	# Kommentar
//	KEY=value
//	export KEY=value
//	KEY="mit Leerzeichen und \n Escapes"
//	KEY='literal, ohne Escapes'
//	KEY=value   # Inline-Kommentar (nur bei unquotierten Werten, Leerzeichen vor #)
//
// Es findet keine Variablen-Expansion statt.
func ParseDotEnv(r io.Reader) (map[string]string, error) {
	out := make(map[string]string)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if lineNo == 1 {
			line = strings.TrimPrefix(line, "\ufeff") // BOM
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))

		key, rawVal, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected KEY=VALUE", lineNo)
		}
		key = strings.TrimSpace(key)
		if !envKeyRe.MatchString(key) {
			return nil, fmt.Errorf("line %d: invalid variable name %q", lineNo, key)
		}
		val, err := parseDotEnvValue(strings.TrimSpace(rawVal))
		if err != nil {
			return nil, fmt.Errorf("line %d (%s): %w", lineNo, key, err)
		}
		out[key] = val
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func parseDotEnvValue(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	switch s[0] {
	case '\'':
		end := strings.IndexByte(s[1:], '\'')
		if end < 0 {
			return "", errors.New("unterminated single quote")
		}
		if err := onlyCommentAfter(s[end+2:]); err != nil {
			return "", err
		}
		return s[1 : end+1], nil

	case '"':
		var b strings.Builder
		for i := 1; i < len(s); i++ {
			switch c := s[i]; c {
			case '\\':
				i++
				if i >= len(s) {
					return "", errors.New("dangling backslash")
				}
				switch s[i] {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				case '"', '\\', '$':
					b.WriteByte(s[i])
				default:
					b.WriteByte('\\')
					b.WriteByte(s[i])
				}
			case '"':
				if err := onlyCommentAfter(s[i+1:]); err != nil {
					return "", err
				}
				return b.String(), nil
			default:
				b.WriteByte(c)
			}
		}
		return "", errors.New("unterminated double quote")

	default:
		// Inline-Kommentar nur, wenn # nach einem Leerzeichen steht.
		// So bleiben Werte wie "pass#word" unverändert.
		if i := strings.Index(s, " #"); i >= 0 {
			s = s[:i]
		}
		return strings.TrimSpace(s), nil
	}
}

func onlyCommentAfter(rest string) error {
	rest = strings.TrimSpace(rest)
	if rest == "" || strings.HasPrefix(rest, "#") {
		return nil
	}
	return fmt.Errorf("unexpected text after closing quote: %q", rest)
}
