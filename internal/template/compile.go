package template

import (
	"errors"
	"fmt"
	htmltemplate "html/template"
	"io"
	"net/mail"
	"regexp"
	"sort"
	"strings"
	texttemplate "text/template"
	"text/template/parse"

	"gopkg.in/yaml.v3"

	"epaper-notificaitons/internal/domain"
)

var (
	nameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	varRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// header ist der YAML-Kopf einer Template-Datei.
type header struct {
	Description string   `yaml:"description"`
	Channel     string   `yaml:"channel"`
	Format      string   `yaml:"format"`
	Subject     string   `yaml:"subject"`
	From        string   `yaml:"from"`
	Required    []string `yaml:"required"`
	Optional    []string `yaml:"optional"`
}

// splitFrontMatter trennt Header und Body. bodyLine ist die Zeile (1-basiert)
// in der Datei, in der der Body beginnt.
func splitFrontMatter(src string) (hdr, body string, bodyLine int, err error) {
	src = strings.TrimPrefix(src, "\ufeff")
	src = strings.ReplaceAll(src, "\r\n", "\n")
	lines := strings.Split(src, "\n")

	if strings.TrimRight(lines[0], " \t") != "---" {
		return "", "", 0, errors.New("file must start with a header block: the first line must be '---'")
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t") == "---" {
			return strings.Join(lines[1:i], "\n"), strings.Join(lines[i+1:], "\n"), i + 2, nil
		}
	}
	return "", "", 0, errors.New("header block is not closed: a second line containing only '---' is missing")
}

// compile prüft eine Template-Datei vollständig und meldet alle Probleme auf
// einmal. Nur bei null Problemen kommt ein Template zurück.
func compile(path, fname, name string, src []byte) (*Template, []string) {
	var probs []string
	add := func(format string, args ...any) {
		probs = append(probs, path+": "+fmt.Sprintf(format, args...))
	}

	hdrText, body, bodyLine, err := splitFrontMatter(string(src))
	if err != nil {
		add("%v", err)
		return nil, probs
	}

	// Ein führender Zeilenumbruch sorgt dafür, dass Zeilennummern in YAML-Fehlern
	// zu den Zeilen der Datei passen (Zeile 1 der Datei ist das öffnende '---').
	var h header
	dec := yaml.NewDecoder(strings.NewReader("\n" + hdrText))
	dec.KnownFields(true)
	if err := dec.Decode(&h); err != nil && !errors.Is(err, io.EOF) {
		add("invalid header: %s", cleanYAMLError(err))
		return nil, probs
	}

	// ---- Header-Felder
	switch {
	case h.Channel == "":
		add("header: 'channel' is required (e.g. channel: email)")
	case !nameRe.MatchString(h.Channel):
		add("header: channel %q is invalid (lowercase letters, digits, '_' and '-' only)", h.Channel)
	}

	format := domain.Format(h.Format)
	switch {
	case h.Format == "":
		add("header: 'format' is required (html or text)")
	case !format.Valid():
		add("header: format %q is invalid (allowed: html, text)", h.Format)
	}

	if strings.TrimSpace(h.Subject) == "" {
		add("header: 'subject' is required")
	}
	if h.From != "" {
		if _, err := mail.ParseAddress(h.From); err != nil {
			add("header: 'from' %q is not a valid address (expected e.g. \"Support <support@example.com>\")", h.From)
		}
	}

	// ---- Variablen
	declared := map[string]bool{}
	owner := map[string]string{}
	checkVars := func(list string, names []string) {
		for _, n := range names {
			switch {
			case !varRe.MatchString(n):
				add("header: variable %q in '%s' is not a valid name (letters, digits and '_', must not start with a digit)", n, list)
			case owner[n] == list:
				add("header: variable %q is listed twice in '%s'", n, list)
			case owner[n] != "":
				add("header: variable %q is listed in both 'required' and 'optional'", n)
			default:
				owner[n] = list
				declared[n] = true
			}
		}
	}
	checkVars("required", h.Required)
	checkVars("optional", h.Optional)

	checkRefs := func(part string, tree *parse.Tree) {
		for _, r := range referencedFields(tree) {
			if declared[r.name] {
				continue
			}
			msg := fmt.Sprintf("%s: references .%s (%s) but it is not declared in 'required' or 'optional'", part, r.name, r.loc)
			if s := suggest(r.name, declared); s != "" {
				msg += fmt.Sprintf(" - did you mean .%s?", s)
			}
			add("%s", msg)
		}
	}

	// ---- Betreff (immer text/template: kein HTML-Escaping in Betreffzeilen)
	var subject *texttemplate.Template
	if strings.TrimSpace(h.Subject) != "" {
		s, err := texttemplate.New(fname + "#subject").Funcs(texttemplate.FuncMap(funcs())).Parse(h.Subject)
		if err != nil {
			add("subject: %v", err)
		} else {
			subject = s
			checkRefs("subject", s.Tree)
		}
	}

	// ---- Body
	if strings.TrimSpace(body) == "" {
		add("body is empty (nothing after the closing '---')")
	}
	// Mit vorangestellten Zeilenumbrüchen stimmen Zeilennummern in Parse-Fehlern
	// mit der Datei überein. Beim Rendern wird der Leerraum wieder entfernt.
	bodySrc := strings.Repeat("\n", bodyLine-1) + body

	var bodyExec executor
	switch format {
	case domain.FormatHTML:
		ht, err := htmltemplate.New(fname).Funcs(htmltemplate.FuncMap(funcs())).Parse(bodySrc)
		if err != nil {
			add("body: %v", err)
			break
		}
		checkRefs("body", ht.Tree) // muss vor dem ersten Execute laufen, das Escaping verändert den Baum
		if len(probs) == 0 {
			if err := dryRunHTML(ht, declared); err != nil {
				add("body: html escaping check failed: %v", err)
			}
		}
		bodyExec = ht

	case domain.FormatText:
		tt, err := texttemplate.New(fname).Funcs(texttemplate.FuncMap(funcs())).Parse(bodySrc)
		if err != nil {
			add("body: %v", err)
			break
		}
		checkRefs("body", tt.Tree)
		bodyExec = tt
	}

	if len(probs) > 0 {
		return nil, probs
	}
	return &Template{
		Name:        name,
		File:        path,
		Description: strings.TrimSpace(h.Description),
		Channel:     h.Channel,
		Format:      format,
		From:        h.From,
		Required:    append([]string{}, h.Required...),
		Optional:    append([]string{}, h.Optional...),
		subject:     subject,
		body:        bodyExec,
	}, nil
}

// dryRunHTML führt das Template einmal mit leeren Werten aus. html/template
// prüft das Kontext-Escaping erst beim ersten Execute (z. B. ein nicht
// geschlossenes Attribut). Damit solche Fehler schon beim Start auffallen,
// wird hier ausgeführt. Laufzeitfehler, die von echten Daten abhängen, sind
// hier bewusst egal. Es zählen nur Escaping-Fehler.
func dryRunHTML(t *htmltemplate.Template, declared map[string]bool) error {
	vars := make(map[string]any, len(declared))
	for k := range declared {
		vars[k] = ""
	}
	err := t.Execute(io.Discard, vars)
	var he *htmltemplate.Error
	if errors.As(err, &he) {
		return err
	}
	return nil
}

// cleanYAMLError macht die Fehlermeldung von yaml.v3 lesbarer
// (ohne den Go-Typnamen und mit einer Zeile pro Problem).
func cleanYAMLError(err error) string {
	msg := err.Error()
	msg = strings.ReplaceAll(msg, " in type template.header", "")
	msg = strings.TrimPrefix(msg, "yaml: unmarshal errors:\n  ")
	msg = strings.ReplaceAll(msg, "\n  ", "; ")
	return msg
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
