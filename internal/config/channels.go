package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ChannelConfig ist die geladene Konfigurationsdatei eines Notification-Channels,
// z. B. config/channels/email.yaml. Der Dateiname (ohne Endung) ist der Channel-Name.
//
// Das Paket weiß bewusst nichts über den Inhalt: Jeder Channel dekodiert seine
// Datei selbst in eine eigene Struktur (siehe Decode). Ein neuer Channel braucht
// dadurch keine Änderung an diesem Paket.
type ChannelConfig struct {
	Name string // "email", "discord", ...
	Path string // Quelldatei, für Fehlermeldungen
	data []byte // YAML nach der ${ENV}-Expansion
}

// Decode dekodiert die Datei streng in v: Unbekannte Felder sind ein Fehler,
// damit Tippfehler in der Konfiguration beim Start auffallen.
func (c ChannelConfig) Decode(v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(c.data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("channel %q (%s): %w", c.Name, c.Path, err)
	}
	return nil
}

var channelNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// LoadChannelConfigs lädt alle *.yaml / *.yml aus dir (nicht rekursiv), sortiert
// nach Channel-Name. Dateien, die mit "." oder "_" beginnen, werden ignoriert,
// damit man Channels zum Beispiel mit "_email.yaml" deaktivieren kann.
//
// In allen String-Werten werden Platzhalter ersetzt:
//
//	${VAR}            Wert der Umgebungsvariable; nicht gesetzt = Fehler
//	${VAR:-default}   Default, wenn VAR nicht gesetzt oder leer ist
//	$${VAR}           ergibt den Literaltext ${VAR}
//
// Die Ersetzung läuft auf dem geparsten YAML, nicht auf dem Rohtext. Dadurch
// können Werte (z. B. Passwörter) beliebige Zeichen wie # oder : enthalten,
// ohne das YAML zu zerstören. Nur Werte werden ersetzt, keine Schlüssel.
func LoadChannelConfigs(dir string, lookup Lookup) ([]ChannelConfig, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read channels dir %q: %w", dir, unwrapPathError(err))
	}

	var (
		out      []ChannelConfig
		problems []string
		seen     = map[string]string{} // Name -> Dateiname
	)
	for _, e := range entries {
		fname := e.Name()
		ext := strings.ToLower(filepath.Ext(fname))
		if e.IsDir() || (ext != ".yaml" && ext != ".yml") ||
			strings.HasPrefix(fname, ".") || strings.HasPrefix(fname, "_") {
			continue
		}
		name := strings.TrimSuffix(fname, filepath.Ext(fname))
		path := filepath.Join(dir, fname)

		if !channelNameRe.MatchString(name) {
			problems = append(problems, fmt.Sprintf("%s: file name must be a lowercase channel name (a-z, 0-9, '_', '-'), e.g. email.yaml", path))
			continue
		}
		if other, dup := seen[name]; dup {
			problems = append(problems, fmt.Sprintf("%s: channel %q is already defined by %s", path, name, other))
			continue
		}
		seen[name] = fname

		raw, err := os.ReadFile(path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", path, unwrapPathError(err)))
			continue
		}
		data, errs := expandYAML(raw, lookup)
		if len(errs) > 0 {
			for _, e := range errs {
				problems = append(problems, fmt.Sprintf("%s: %s", path, e))
			}
			continue
		}
		out = append(out, ChannelConfig{Name: name, Path: path, data: data})
	}

	if len(problems) > 0 {
		return nil, &Error{Problems: problems}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// expandYAML parst raw, ersetzt Platzhalter in allen Skalar-Werten und
// serialisiert das Ergebnis wieder.
func expandYAML(raw []byte, lookup Lookup) ([]byte, []string) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, []string{"invalid YAML: " + err.Error()}
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return nil, []string{"file is empty"}
	}

	var problems []string
	expandNode(&doc, "", lookup, &problems)
	if len(problems) > 0 {
		return nil, problems
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, []string{"re-encoding YAML failed: " + err.Error()}
	}
	return out, nil
}

func expandNode(n *yaml.Node, path string, lookup Lookup, problems *[]string) {
	const quoted = yaml.DoubleQuotedStyle | yaml.SingleQuotedStyle | yaml.LiteralStyle | yaml.FoldedStyle

	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for i, c := range n.Content {
			p := path
			if n.Kind == yaml.SequenceNode {
				p = fmt.Sprintf("%s[%d]", path, i)
			}
			expandNode(c, p, lookup, problems)
		}

	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i].Value
			p := key
			if path != "" {
				p = path + "." + key
			}
			expandNode(n.Content[i+1], p, lookup, problems)
		}

	case yaml.ScalarNode:
		if !strings.Contains(n.Value, "${") && !strings.Contains(n.Value, "$${") {
			return
		}
		val, missing := expandString(n.Value, lookup)
		for _, name := range missing {
			*problems = append(*problems, fmt.Sprintf("%s: environment variable %s is not set (use ${%s:-default} to make it optional)", path, name, name))
		}
		n.Value = val
		// Unquotierte Werte wie `port: ${SMTP_PORT}` sollen nach der Ersetzung
		// wieder als Zahl/Bool erkannt werden können. Tag leeren = neu auflösen.
		if n.Style&quoted == 0 {
			n.Tag = ""
		}
	}
	// AliasNode: wird dort expandiert, wo der Anker definiert ist.
}

var envRefRe = regexp.MustCompile(`(\$)?\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// expandString ersetzt ${VAR}-Platzhalter. missing listet Variablen ohne Wert und ohne Default.
func expandString(s string, lookup Lookup) (result string, missing []string) {
	result = envRefRe.ReplaceAllStringFunc(s, func(match string) string {
		m := envRefRe.FindStringSubmatch(match)
		escape, name, def := m[1], m[2], m[3]
		hasDefault := strings.Contains(match, ":-")

		if escape != "" { // $${VAR} -> Literal ${VAR}
			return match[1:]
		}
		v, set := lookup(name)
		switch {
		case hasDefault && (!set || v == ""):
			return def
		case !set:
			missing = append(missing, name)
			return ""
		default:
			return v
		}
	})
	return result, missing
}

// ErrNoChannels wird vom Start benutzt, wenn keine einzige Channel-Datei gefunden wurde.
var ErrNoChannels = errors.New("no channel configuration found")
