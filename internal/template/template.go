// Package template lädt, validiert und rendert die Nachrichten-Templates
// (Ordner templates/, eine *.tmpl-Datei pro Template).
//
// Alle Templates werden beim Start geladen und vollständig geprüft (Header,
// Syntax, verwendete Variablen, HTML-Escaping). Ein kaputtes Template
// verhindert den Start, statt erst beim ersten Versand aufzufallen.
//
// Dateiformat, siehe docs/TEMPLATES.md:
//
//	---
//	channel: email
//	format: html            # html | text
//	subject: "Hallo {{.user_name}}"
//	required: [user_name]
//	optional: [expires_in]
//	---
//	<p>Body als Go-Template ...</p>
package template

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	texttemplate "text/template"

	"epaper-notificaitons/internal/domain"
)

const fileExt = ".tmpl"

// ErrUnknownTemplate wird zurückgegeben, wenn ein Template nicht existiert.
var ErrUnknownTemplate = errors.New("unknown template")

// LoadError sammelt alle Probleme beim Laden, damit man nicht einen Fehler
// nach dem anderen beheben muss.
type LoadError struct {
	Problems []string
}

func (e *LoadError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "invalid templates (%d problem", len(e.Problems))
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

// MissingVarsError meldet fehlende Pflichtvariablen. Missing ist sortiert.
// Die API übersetzt das später in HTTP 422 mit genau dieser Liste.
type MissingVarsError struct {
	Template string
	Missing  []string
}

func (e *MissingVarsError) Error() string {
	return fmt.Sprintf("template %q: missing required variables: %s", e.Template, strings.Join(e.Missing, ", "))
}

// RenderError beschreibt einen Fehler beim Rendern von Betreff oder Body.
type RenderError struct {
	Template string
	Part     string // "subject" | "body"
	Err      error
}

func (e *RenderError) Error() string {
	return fmt.Sprintf("render template %q (%s): %v", e.Template, e.Part, e.Err)
}

func (e *RenderError) Unwrap() error { return e.Err }

// Info beschreibt ein Template nach außen, z. B. für GET /v1/templates.
type Info struct {
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Channel     string        `json:"channel"`
	Format      domain.Format `json:"format"`
	Required    []string      `json:"required"`
	Optional    []string      `json:"optional"`
}

// Rendered ist das Ergebnis von Render und geht an den Channel.
type Rendered struct {
	Template string
	Channel  string
	Format   domain.Format
	Subject  string // einzeilig, ohne Zeilenumbrüche
	Body     string
	From     string // leer = Default des Channels
}

type executor interface {
	Execute(w io.Writer, data any) error
}

// Template ist ein geladenes, geprüftes Template. Es ist nach dem Laden
// unveränderlich und für parallele Nutzung sicher.
type Template struct {
	Name        string
	File        string
	Description string
	Channel     string
	Format      domain.Format
	From        string
	Required    []string
	Optional    []string

	subject *texttemplate.Template
	body    executor
}

// Info liefert eine Kopie der öffentlichen Beschreibung.
func (t *Template) Info() Info {
	return Info{
		Name:        t.Name,
		Description: t.Description,
		Channel:     t.Channel,
		Format:      t.Format,
		Required:    append([]string{}, t.Required...),
		Optional:    append([]string{}, t.Optional...),
	}
}

// Missing liefert die sortierte Liste der Pflichtvariablen, die in data fehlen.
// Als fehlend gilt eine Variable, wenn sie nicht vorhanden, null oder ein leerer
// bzw. nur aus Leerraum bestehender String ist. 0 und false zählen als vorhanden.
func (t *Template) Missing(data map[string]any) []string {
	var missing []string
	for _, name := range t.Required {
		if isBlank(data[name]) {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

// Render prüft die Pflichtvariablen und erzeugt Betreff und Body.
//
// Fehler: *MissingVarsError bei fehlenden Pflichtvariablen, *RenderError wenn
// das Template zur Laufzeit scheitert oder ein leeres Ergebnis liefert.
func (t *Template) Render(data map[string]any) (*Rendered, error) {
	if missing := t.Missing(data); len(missing) > 0 {
		return nil, &MissingVarsError{Template: t.Name, Missing: missing}
	}
	vars := t.execData(data)

	var subj bytes.Buffer
	if err := t.subject.Execute(&subj, vars); err != nil {
		return nil, &RenderError{Template: t.Name, Part: "subject", Err: err}
	}
	var body bytes.Buffer
	if err := t.body.Execute(&body, vars); err != nil {
		return nil, &RenderError{Template: t.Name, Part: "body", Err: err}
	}

	// Zeilenumbrüche im Betreff würden Mail-Header-Injection ermöglichen (z. B.
	// "Hi\r\nBcc: ..." aus einem Benutzernamen). Jeder Leerraum wird zu einem Leerzeichen.
	subject := strings.Join(strings.Fields(subj.String()), " ")
	if subject == "" {
		return nil, &RenderError{Template: t.Name, Part: "subject", Err: errors.New("subject is empty after rendering")}
	}
	text := strings.TrimSpace(body.String())
	if text == "" {
		return nil, &RenderError{Template: t.Name, Part: "body", Err: errors.New("body is empty after rendering")}
	}

	return &Rendered{
		Template: t.Name,
		Channel:  t.Channel,
		Format:   t.Format,
		Subject:  subject,
		Body:     text,
		From:     t.From,
	}, nil
}

// execData kopiert data flach und setzt fehlende optionale Variablen auf "".
// Ohne das gäbe text/template für fehlende Map-Einträge "<no value>" aus.
func (t *Template) execData(data map[string]any) map[string]any {
	vars := make(map[string]any, len(data)+len(t.Optional))
	for k, v := range data {
		vars[k] = v
	}
	for _, k := range t.Optional {
		if v, ok := vars[k]; !ok || v == nil {
			vars[k] = ""
		}
	}
	return vars
}

// Registry hält alle geladenen Templates. Nach Load ist es unveränderlich.
type Registry struct {
	byName  map[string]*Template
	names   []string
	ignored []string
}

// Load liest alle *.tmpl aus dir (nicht rekursiv). Dateien, die mit "." oder "_"
// beginnen, und *.md werden stillschweigend übersprungen (so lässt sich ein
// Template deaktivieren). Andere Dateien landen in Ignored(), damit ein
// versehentlich falsch benanntes Template nicht unbemerkt bleibt.
func Load(dir string) (*Registry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		var pe *fs.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return nil, fmt.Errorf("read templates dir %q: %w", dir, err)
	}

	reg := &Registry{byName: make(map[string]*Template)}
	var problems []string

	for _, e := range entries {
		fname := e.Name()
		if e.IsDir() || strings.HasPrefix(fname, ".") || strings.HasPrefix(fname, "_") {
			continue
		}
		ext := strings.ToLower(filepath.Ext(fname))
		if ext == ".md" {
			continue
		}
		if ext != fileExt {
			reg.ignored = append(reg.ignored, fname)
			continue
		}

		name := strings.TrimSuffix(fname, filepath.Ext(fname))
		path := filepath.Join(dir, fname)
		if !nameRe.MatchString(name) {
			problems = append(problems, fmt.Sprintf("%s: file name must be a lowercase template name (a-z, 0-9, '_', '-'), e.g. password_reset.tmpl", path))
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			var pe *fs.PathError
			if errors.As(err, &pe) {
				err = pe.Err
			}
			problems = append(problems, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		t, probs := compile(path, fname, name, src)
		if len(probs) > 0 {
			problems = append(problems, probs...)
			continue
		}
		reg.byName[name] = t
		reg.names = append(reg.names, name)
	}

	if len(problems) > 0 {
		return nil, &LoadError{Problems: problems}
	}
	sort.Strings(reg.names)
	return reg, nil
}

// Get liefert das Template mit dem Namen (Dateiname ohne .tmpl).
func (r *Registry) Get(name string) (*Template, bool) {
	t, ok := r.byName[name]
	return t, ok
}

// Len ist die Anzahl geladener Templates.
func (r *Registry) Len() int { return len(r.names) }

// Names liefert die sortierten Template-Namen.
func (r *Registry) Names() []string { return append([]string(nil), r.names...) }

// List liefert die Beschreibung aller Templates, sortiert nach Name.
func (r *Registry) List() []Info {
	out := make([]Info, 0, len(r.names))
	for _, n := range r.names {
		out = append(out, r.byName[n].Info())
	}
	return out
}

// Ignored liefert Dateien im Template-Ordner, die keine Templates sind
// (Endung nicht .tmpl). Der Aufrufer sollte sie als Warnung loggen.
func (r *Registry) Ignored() []string { return append([]string(nil), r.ignored...) }

// Render rendert das Template name. Unbekannte Namen liefern ErrUnknownTemplate.
func (r *Registry) Render(name string, data map[string]any) (*Rendered, error) {
	t, ok := r.byName[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownTemplate, name)
	}
	return t.Render(data)
}
