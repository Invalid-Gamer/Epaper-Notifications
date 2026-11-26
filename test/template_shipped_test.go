package test

import (
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"epaper-notificaitons/internal/domain"
	"epaper-notificaitons/internal/template"
)

// Beispieldaten für jedes mitgelieferte Template. Kommt ein neues Template in
// templates/ dazu, schlägt TestShippedTemplatesAreAllCovered fehl, bis hier
// Beispieldaten eingetragen sind.
var shippedSamples = map[string]struct {
	format   domain.Format
	data     map[string]any
	subject  string   // erwarteter Betreff
	contains []string // muss im Body vorkommen
}{
	"password_reset": {
		format:   domain.FormatHTML,
		data:     map[string]any{"user_name": "Max", "reset_link": "https://example.com/reset?t=abc", "expires_in": "1 Stunde"},
		subject:  "Passwort zurücksetzen",
		contains: []string{"Hallo Max", "https://example.com/reset?t=abc", "1 Stunde"},
	},
	"verify_email": {
		format:   domain.FormatHTML,
		data:     map[string]any{"user_name": "Max", "confirm_link": "https://example.com/confirm/xyz"},
		subject:  "Bitte bestätige deine E-Mail-Adresse",
		contains: []string{"Hallo Max", "https://example.com/confirm/xyz"},
	},
	"two_factor": {
		format:   domain.FormatText,
		data:     map[string]any{"code": "493 021", "user_name": "Max", "ip_address": "203.0.113.7"},
		subject:  "Dein Sicherheitscode",
		contains: []string{"Hallo Max,", "493 021", "203.0.113.7"},
	},
	"alert": {
		format:   domain.FormatText,
		data:     map[string]any{"title": "Disk voll", "message": "Auf /var sind noch 2% frei.", "severity": "CRITICAL", "source": "db-01"},
		subject:  "[CRITICAL] Disk voll",
		contains: []string{"Disk voll", "Schweregrad: CRITICAL", "Quelle:      db-01", "Auf /var sind noch 2% frei."},
	},
	"support": {
		format:   domain.FormatHTML,
		data:     map[string]any{"user_name": "Max", "ticket_id": "T-1042", "ticket_subject": "Login geht nicht", "message": "Bitte lösche den Cache.", "agent_name": "Anna"},
		subject:  "Re: Login geht nicht [Ticket T-1042]",
		contains: []string{"Hallo Max", "Bitte lösche den Cache.", "Anna"},
	},
}

func shippedRegistry(t *testing.T) *template.Registry {
	t.Helper()
	reg, err := template.Load("../templates")
	if err != nil {
		t.Fatalf("shipped templates are invalid:\n%v", err)
	}
	return reg
}

func TestShippedTemplatesAreAllCovered(t *testing.T) {
	reg := shippedRegistry(t)
	var want []string
	for name := range shippedSamples {
		want = append(want, name)
	}
	sort.Strings(want)
	if got := reg.Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("templates/ and shippedSamples differ:\n  templates/:     %v\n  shippedSamples: %v\n"+
			"(neues Template? Dann Beispieldaten in test/template_shipped_test.go ergänzen)", got, want)
	}
	if ig := reg.Ignored(); len(ig) != 0 {
		t.Errorf("non-template files in templates/: %v", ig)
	}
}

func TestShippedTemplatesRender(t *testing.T) {
	reg := shippedRegistry(t)
	for name, s := range shippedSamples {
		t.Run(name, func(t *testing.T) {
			got, err := reg.Render(name, s.data)
			if err != nil {
				t.Fatal(err)
			}
			if got.Subject != s.subject {
				t.Errorf("subject = %q, want %q", got.Subject, s.subject)
			}
			if got.Format != s.format || got.Channel != "email" {
				t.Errorf("format/channel = %s/%s", got.Format, got.Channel)
			}
			for _, c := range s.contains {
				if !strings.Contains(got.Body, c) {
					t.Errorf("body does not contain %q:\n%s", c, got.Body)
				}
			}
			if strings.Contains(got.Body, "no value") || strings.Contains(got.Body, "{{") {
				t.Errorf("unrendered placeholder in body:\n%s", got.Body)
			}
			if got.Format == domain.FormatHTML && !strings.Contains(got.Body, "<html") {
				t.Errorf("html template without <html> element:\n%s", got.Body)
			}
		})
	}
}

// Jede Pflichtvariable wird einzeln weggelassen: Das muss genau sie melden.
func TestShippedTemplatesRequireTheirVariables(t *testing.T) {
	reg := shippedRegistry(t)
	for name, s := range shippedSamples {
		tpl, _ := reg.Get(name)
		for _, req := range tpl.Required {
			t.Run(name+"/"+req, func(t *testing.T) {
				data := map[string]any{}
				for k, v := range s.data {
					if k != req {
						data[k] = v
					}
				}
				_, err := reg.Render(name, data)
				var mv *template.MissingVarsError
				if !errors.As(err, &mv) || !reflect.DeepEqual(mv.Missing, []string{req}) {
					t.Fatalf("want MissingVarsError{%s}, got %v", req, err)
				}
			})
		}
	}
}

// Die Beispieldaten müssen jede Pflichtvariable abdecken, sonst testet obiges nichts.
func TestShippedSamplesCoverAllRequiredVariables(t *testing.T) {
	reg := shippedRegistry(t)
	for name, s := range shippedSamples {
		tpl, _ := reg.Get(name)
		if missing := tpl.Missing(s.data); len(missing) != 0 {
			t.Errorf("%s: sample data lacks required %v", name, missing)
		}
	}
}

// HTML-Templates dürfen eingesetzte Daten nie unescaped ausgeben.
func TestShippedHTMLTemplatesEscapeData(t *testing.T) {
	reg := shippedRegistry(t)
	for name, s := range shippedSamples {
		if s.format != domain.FormatHTML {
			continue
		}
		t.Run(name, func(t *testing.T) {
			data := map[string]any{}
			for k := range s.data {
				data[k] = "<script>alert(1)</script>"
			}
			got, err := reg.Render(name, data)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(got.Body, "<script>") {
				t.Errorf("unescaped data in body:\n%s", got.Body)
			}
		})
	}
}

// Optionale Variablen dürfen fehlen, ohne dass etwas Kaputtes im Text steht.
func TestShippedTemplatesWorkWithOnlyRequiredVariables(t *testing.T) {
	reg := shippedRegistry(t)
	for name, s := range shippedSamples {
		t.Run(name, func(t *testing.T) {
			tpl, _ := reg.Get(name)
			data := map[string]any{}
			for _, r := range tpl.Required {
				data[r] = s.data[r]
			}
			got, err := reg.Render(name, data)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(got.Body, "no value") || strings.Contains(got.Subject, "no value") {
				t.Errorf("placeholder leaked:\nsubject=%q\nbody=%s", got.Subject, got.Body)
			}
		})
	}
}
