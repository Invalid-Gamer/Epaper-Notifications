package test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"epaper-notificaitons/internal/domain"
	"epaper-notificaitons/internal/template"
)

// ---------------------------------------------------------------- Helfer

func tplWrite(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func tplLoad(t *testing.T, files map[string]string) (*template.Registry, error) {
	t.Helper()
	return template.Load(tplWrite(t, files))
}

func tplMustLoad(t *testing.T, files map[string]string) *template.Registry {
	t.Helper()
	reg, err := tplLoad(t, files)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return reg
}

func tplProblems(t *testing.T, err error) []string {
	t.Helper()
	var le *template.LoadError
	if !errors.As(err, &le) {
		t.Fatalf("expected *template.LoadError, got %T: %v", err, err)
	}
	return le.Problems
}

func tplHas(problems []string, substr string) bool {
	for _, p := range problems {
		if strings.Contains(p, substr) {
			return true
		}
	}
	return false
}

// tplFile baut eine Template-Datei aus Header-Zeilen und Body.
func tplFile(header, body string) string {
	return "---\n" + header + "\n---\n" + body
}

const tplBasicHeader = "channel: email\nformat: text\nsubject: \"s\"\nrequired: [a]"

// ---------------------------------------------------------------- Rendern

func TestTemplateRenderHTMLEscapesBodyButNotSubject(t *testing.T) {
	reg := tplMustLoad(t, map[string]string{"greet.tmpl": tplFile(
		"channel: email\nformat: html\nsubject: \"Hallo {{.name}}\"\nrequired: [name]\noptional: [note]",
		"<p>Hi {{.name}}</p>{{if .note}}<p>{{.note}}</p>{{end}}\n")})

	got, err := reg.Render("greet", map[string]any{"name": "<b>Tom & Jerry</b>"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != "<p>Hi &lt;b&gt;Tom &amp; Jerry&lt;/b&gt;</p>" {
		t.Errorf("body = %q", got.Body)
	}
	if got.Subject != "Hallo <b>Tom & Jerry</b>" {
		t.Errorf("subject must not be HTML-escaped: %q", got.Subject)
	}
	if got.Template != "greet" || got.Channel != "email" || got.Format != domain.FormatHTML || got.From != "" {
		t.Errorf("metadata: %+v", got)
	}
}

func TestTemplateRenderTextIsNotEscaped(t *testing.T) {
	reg := tplMustLoad(t, map[string]string{"plain.tmpl": tplFile(tplBasicHeader, "Wert: {{.a}}\n")})
	got, err := reg.Render("plain", map[string]any{"a": "<b>&</b>"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != "Wert: <b>&</b>" {
		t.Errorf("body = %q", got.Body)
	}
}

func TestTemplateOptionalMissingRendersEmpty(t *testing.T) {
	reg := tplMustLoad(t, map[string]string{"opt.tmpl": tplFile(
		"channel: email\nformat: text\nsubject: s\noptional: [o]",
		"Hallo{{if .o}} {{.o}}{{end}}!{{.o}}\n")})

	got, err := reg.Render("opt", nil) // data == nil ist erlaubt
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != "Hallo!" || strings.Contains(got.Body, "no value") {
		t.Errorf("body = %q", got.Body)
	}

	got, _ = reg.Render("opt", map[string]any{"o": nil}) // explizites null wie fehlend
	if got.Body != "Hallo!" {
		t.Errorf("null body = %q", got.Body)
	}
	got, _ = reg.Render("opt", map[string]any{"o": "Max"})
	if got.Body != "Hallo Max!Max" {
		t.Errorf("with value body = %q", got.Body)
	}
}

func TestTemplateMissingRequiredVariables(t *testing.T) {
	reg := tplMustLoad(t, map[string]string{"m.tmpl": tplFile(
		"channel: email\nformat: text\nsubject: s\nrequired: [c, a, b, d, e]",
		"{{.a}}{{.b}}{{.c}}{{.d}}{{.e}}")})

	_, err := reg.Render("m", map[string]any{
		"a": "x",
		"b": nil,     // null = fehlt
		"c": "  \t ", // nur Leerraum = fehlt
		// d fehlt ganz
		"e": 0, // 0 zählt als angegeben
	})
	var mv *template.MissingVarsError
	if !errors.As(err, &mv) {
		t.Fatalf("expected *MissingVarsError, got %T: %v", err, err)
	}
	if !reflect.DeepEqual(mv.Missing, []string{"b", "c", "d"}) {
		t.Errorf("Missing = %v (want sorted [b c d])", mv.Missing)
	}
	if mv.Template != "m" || !strings.Contains(err.Error(), "b, c, d") {
		t.Errorf("error: %v", err)
	}

	// Template.Missing liefert dasselbe ohne zu rendern (für die API-Validierung).
	tm, _ := reg.Get("m")
	if got := tm.Missing(map[string]any{"a": 1, "b": 1, "c": 1, "d": 1, "e": false}); len(got) != 0 {
		t.Errorf("false must count as present, Missing = %v", got)
	}
}

func TestTemplateUnknown(t *testing.T) {
	reg := tplMustLoad(t, map[string]string{"x.tmpl": tplFile(tplBasicHeader, "{{.a}}")})
	_, err := reg.Render("nope", map[string]any{})
	if !errors.Is(err, template.ErrUnknownTemplate) {
		t.Fatalf("got %v", err)
	}
	if _, ok := reg.Get("nope"); ok {
		t.Error("Get(nope) must be false")
	}
}

func TestTemplateJavascriptURLIsNeutralised(t *testing.T) {
	reg := tplMustLoad(t, map[string]string{"link.tmpl": tplFile(
		"channel: email\nformat: html\nsubject: s\nrequired: [link]",
		`<a href="{{.link}}">x</a>`)})

	got, err := reg.Render("link", map[string]any{"link": "javascript:alert(1)"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Body, "javascript:") || !strings.Contains(got.Body, "#ZgotmplZ") {
		t.Errorf("dangerous URL not neutralised: %q", got.Body)
	}

	got, _ = reg.Render("link", map[string]any{"link": "https://example.com/a?b=c&d=e"})
	if !strings.Contains(got.Body, `href="https://example.com/a?b=c&amp;d=e"`) {
		t.Errorf("normal URL mangled: %q", got.Body)
	}
}

func TestTemplateSubjectHeaderInjection(t *testing.T) {
	reg := tplMustLoad(t, map[string]string{"s.tmpl": tplFile(
		"channel: email\nformat: text\nsubject: \"Hi {{.s}}\"\nrequired: [s]", "body")})

	got, err := reg.Render("s", map[string]any{"s": "Max\r\nBcc: evil@example.com\n\tmore"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(got.Subject, "\r\n\t") {
		t.Errorf("subject contains control whitespace: %q", got.Subject)
	}
	if got.Subject != "Hi Max Bcc: evil@example.com more" {
		t.Errorf("subject = %q", got.Subject)
	}
}

func TestTemplateEmptyResultsAreErrors(t *testing.T) {
	reg := tplMustLoad(t, map[string]string{
		"nosubject.tmpl": tplFile("channel: email\nformat: text\nsubject: \"{{.o}}\"\noptional: [o]", "body"),
		"nobody.tmpl":    tplFile("channel: email\nformat: text\nsubject: s\noptional: [o]", "{{.o}}"),
	})
	for name, part := range map[string]string{"nosubject": "subject", "nobody": "body"} {
		_, err := reg.Render(name, nil)
		var re *template.RenderError
		if !errors.As(err, &re) || re.Part != part || re.Template != name {
			t.Errorf("%s: expected RenderError for %s, got %T: %v", name, part, err, err)
			continue
		}
		if !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), part) {
			t.Errorf("%s: error message lacks template/part: %v", name, err)
		}
		if errors.Unwrap(err) == nil {
			t.Errorf("%s: RenderError must wrap its cause", name)
		}
	}
}

func TestTemplateDefaultFunc(t *testing.T) {
	reg := tplMustLoad(t, map[string]string{"d.tmpl": tplFile(
		"channel: email\nformat: text\nsubject: 'Hallo {{default \"Kunde\" .name}}'\noptional: [name]",
		"{{default \"-\" .name}}")})

	got, _ := reg.Render("d", nil)
	if got.Subject != "Hallo Kunde" || got.Body != "-" {
		t.Errorf("fallback: %q / %q", got.Subject, got.Body)
	}
	got, _ = reg.Render("d", map[string]any{"name": "Max"})
	if got.Subject != "Hallo Max" || got.Body != "Max" {
		t.Errorf("value: %q / %q", got.Subject, got.Body)
	}
}

func TestTemplateRangeAndWithScope(t *testing.T) {
	// Felder im range-Körper sind Elementfelder und dürfen nicht als
	// "nicht deklarierte Variablen" gemeldet werden. $.name zeigt auf die Wurzel.
	reg := tplMustLoad(t, map[string]string{"r.tmpl": tplFile(
		"channel: email\nformat: text\nsubject: s\nrequired: [items, title]",
		"{{range .items}}{{.name}}:{{$.title}};{{end}}")})

	got, err := reg.Render("r", map[string]any{
		"title": "T",
		"items": []any{map[string]any{"name": "a"}, map[string]any{"name": "b"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != "a:T;b:T;" {
		t.Errorf("body = %q", got.Body)
	}

	_, err = tplLoad(t, map[string]string{"r2.tmpl": tplFile(
		"channel: email\nformat: text\nsubject: s\nrequired: [items]",
		"{{range .items}}{{$.nope}}{{end}}")})
	if !tplHas(tplProblems(t, err), "references .nope") {
		t.Errorf("$.nope inside range not detected: %v", err)
	}
}

func TestTemplateConcurrentRender(t *testing.T) {
	reg := tplMustLoad(t, map[string]string{"c.tmpl": tplFile(
		"channel: email\nformat: html\nsubject: \"Hi {{.n}}\"\nrequired: [n]", "<p>{{.n}}</p>")})

	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			n := fmt.Sprintf("user<%d>", i)
			got, err := reg.Render("c", map[string]any{"n": n})
			want := fmt.Sprintf("<p>user&lt;%d&gt;</p>", i)
			if err != nil || got.Body != want {
				errs <- fmt.Sprintf("i=%d err=%v body=%q", i, err, got)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

// ---------------------------------------------------------------- Laden: Fehlerfälle

func TestTemplateLoadProblems(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string // alle Teilstrings müssen in den Problemen vorkommen
	}{
		{"no header", "Hallo", []string{"first line must be '---'"}},
		{"unclosed header", "---\nchannel: email\n", []string{"not closed"}},
		{"unknown header key", tplFile("channel: email\nformat: text\nsubject: s\nsubjct: typo", "x"),
			[]string{"invalid header", "subjct"}},
		{"wrong header type", tplFile("channel: email\nformat: text\nsubject: s\nrequired: foo", "x"),
			[]string{"invalid header"}},
		{"all three mandatory fields missing", tplFile("description: nur Beschreibung", "x"),
			[]string{"'channel' is required", "'format' is required", "'subject' is required"}},
		{"empty header", "---\n---\nx", []string{"'channel' is required"}},
		{"bad channel name", tplFile("channel: E-Mail!\nformat: text\nsubject: s", "x"), []string{`channel "E-Mail!" is invalid`}},
		{"bad format", tplFile("channel: email\nformat: pdf\nsubject: s", "x"), []string{`format "pdf" is invalid`}},
		{"bad from", tplFile("channel: email\nformat: text\nsubject: s\nfrom: not-an-address", "x"), []string{"'from'"}},
		{"invalid var name", tplFile("channel: email\nformat: text\nsubject: s\nrequired: [bad-name]", "x"), []string{"not a valid name"}},
		{"duplicate var", tplFile("channel: email\nformat: text\nsubject: s\nrequired: [a, a]", "{{.a}}"), []string{"listed twice"}},
		{"var in both lists", tplFile("channel: email\nformat: text\nsubject: s\nrequired: [a]\noptional: [a]", "{{.a}}"), []string{"both 'required' and 'optional'"}},
		{"empty body", tplFile(tplBasicHeader, "  \n\n"), []string{"body is empty"}},
		{"body syntax error", tplFile(tplBasicHeader, "{{if .a}}offen"), []string{"body:", "unexpected"}},
		{"subject syntax error", tplFile("channel: email\nformat: text\nsubject: '{{.a'\nrequired: [a]", "{{.a}}"), []string{"subject:"}},
		{"unknown function", tplFile(tplBasicHeader, "{{.a | nofunc}}"), []string{`function "nofunc" not defined`}},
		{"undeclared variable with suggestion",
			tplFile("channel: email\nformat: text\nsubject: s\nrequired: [user_name]", "Hi {{.usr_name}}"),
			[]string{"body: references .usr_name", "did you mean .user_name?"}},
		{"undeclared variable in subject",
			tplFile("channel: email\nformat: text\nsubject: '{{.nope}}'\nrequired: [a]", "{{.a}}"),
			[]string{"subject: references .nope"}},
		{"undeclared variable far from any declared one",
			tplFile("channel: email\nformat: text\nsubject: s\nrequired: [a]", "{{.a}}{{.zzzzzz}}"),
			[]string{"references .zzzzzz"}},
		{"html escaping error (unclosed attribute)",
			tplFile("channel: email\nformat: html\nsubject: s\nrequired: [x]", `<a href="{{.x}}>link</a>`),
			[]string{"html escaping check failed"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tplLoad(t, map[string]string{"bad.tmpl": tt.content})
			ps := tplProblems(t, err)
			for _, w := range tt.want {
				if !tplHas(ps, w) {
					t.Errorf("problems %q do not contain %q", ps, w)
				}
			}
			for _, p := range ps {
				if !strings.Contains(p, "bad.tmpl") {
					t.Errorf("problem does not name the file: %q", p)
				}
			}
		})
	}
}

func TestTemplateLoadDeclaredButUnsuggestedIsNotMisleading(t *testing.T) {
	_, err := tplLoad(t, map[string]string{"bad.tmpl": tplFile(tplBasicHeader, "{{.a}}{{.zzzzzz}}")})
	for _, p := range tplProblems(t, err) {
		if strings.Contains(p, "did you mean") {
			t.Errorf("unrelated suggestion offered: %q", p)
		}
	}
}

func TestTemplateErrorLinesMatchTheFile(t *testing.T) {
	// Zeile 9 der Datei enthält die unbekannte Funktion.
	content := "---\n" + // 1
		"channel: email\n" + // 2
		"format: text\n" + // 3
		"subject: \"x\"\n" + // 4
		"required: [a]\n" + // 5
		"---\n" + // 6
		"Zeile sieben\n" + // 7
		"Zeile acht\n" + // 8
		"{{ .a | nofunc }}\n" // 9
	_, err := tplLoad(t, map[string]string{"bad.tmpl": content})
	if !tplHas(tplProblems(t, err), "bad.tmpl:9:") {
		t.Errorf("expected line 9 in error, got %v", err)
	}

	// Dasselbe für nicht deklarierte Variablen in Zeile 8 (HTML-Pfad, anderer Parser).
	content = "---\nchannel: email\nformat: html\nsubject: s\nrequired: [a]\n---\n<p>\n{{.missing}}\n</p>\n"
	_, err = tplLoad(t, map[string]string{"bad.tmpl": content})
	if !tplHas(tplProblems(t, err), "bad.tmpl:8:") {
		t.Errorf("expected line 8 for undeclared variable, got %v", err)
	}

	// YAML-Fehler im Header: Zeile 4 der Datei.
	content = "---\nchannel: email\nformat: text\nbogus: 1\n---\nx"
	_, err = tplLoad(t, map[string]string{"bad.tmpl": content})
	if !tplHas(tplProblems(t, err), "line 4") {
		t.Errorf("expected header line 4, got %v", err)
	}
}

func TestTemplateLoadReportsAllFilesAtOnce(t *testing.T) {
	_, err := tplLoad(t, map[string]string{
		"good.tmpl":     tplFile(tplBasicHeader, "{{.a}}"),
		"bad1.tmpl":     "kein header",
		"bad2.tmpl":     tplFile("channel: email\nformat: nope\nsubject: s", "x"),
		"Bad Name.tmpl": tplFile(tplBasicHeader, "{{.a}}"),
	})
	ps := tplProblems(t, err)
	for _, want := range []string{"bad1.tmpl", "bad2.tmpl", "file name must be a lowercase template name"} {
		if !tplHas(ps, want) {
			t.Errorf("missing %q in %q", want, ps)
		}
	}
	if tplHas(ps, "good.tmpl") {
		t.Errorf("valid file reported as problem: %q", ps)
	}
	if !strings.Contains(err.Error(), "3 problems") {
		t.Errorf("message: %s", err)
	}
}

func TestTemplateFileSelection(t *testing.T) {
	dir := tplWrite(t, map[string]string{
		"a.tmpl":       tplFile(tplBasicHeader, "{{.a}}"),
		"b.TMPL":       tplFile(tplBasicHeader, "{{.a}}"), // Endung ohne Rücksicht auf Groß-/Kleinschreibung
		"_draft.tmpl":  "kaputt, wird übersprungen",
		".hidden.tmpl": "kaputt, wird übersprungen",
		"README.md":    "# Doku",
		"notes.txt":    "falsch benannt",
		"welcome.html": "falsch benannt",
	})
	if err := os.Mkdir(filepath.Join(dir, "sub.tmpl"), 0o700); err != nil {
		t.Fatal(err)
	}
	reg, err := template.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.Names(); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("Names = %v", got)
	}
	if got := reg.Ignored(); !reflect.DeepEqual(got, []string{"notes.txt", "welcome.html"}) {
		t.Errorf("Ignored = %v (falsch benannte Dateien sollen gemeldet werden)", got)
	}
}

func TestTemplateLoadEmptyAndMissingDir(t *testing.T) {
	reg, err := template.Load(t.TempDir())
	if err != nil || reg.Len() != 0 {
		t.Fatalf("empty dir: len=%d err=%v", reg.Len(), err)
	}
	_, err = template.Load("/definitely/not/here")
	if err == nil || !strings.Contains(err.Error(), "/definitely/not/here") {
		t.Fatalf("missing dir: %v", err)
	}
}

// ---------------------------------------------------------------- Beschreibung

func TestTemplateListAndInfo(t *testing.T) {
	reg := tplMustLoad(t, map[string]string{
		"zeta.tmpl": tplFile("description: Letztes\nchannel: email\nformat: html\nsubject: s\nrequired: [a, b]\noptional: [c]",
			"{{.a}}{{.b}}{{.c}}"),
		"alpha.tmpl": tplFile("channel: ntfy\nformat: text\nsubject: s", "Nur Text"),
	})
	list := reg.List()
	if len(list) != 2 || list[0].Name != "alpha" || list[1].Name != "zeta" {
		t.Fatalf("List not sorted: %+v", list)
	}
	alpha, zeta := list[0], list[1]
	if alpha.Channel != "ntfy" || alpha.Format != domain.FormatText || alpha.Description != "" {
		t.Errorf("alpha: %+v", alpha)
	}
	// Leere Listen müssen nicht-nil sein, damit JSON [] statt null ausgibt.
	if alpha.Required == nil || alpha.Optional == nil {
		t.Errorf("nil slices in Info: %+v", alpha)
	}
	if zeta.Description != "Letztes" || !reflect.DeepEqual(zeta.Required, []string{"a", "b"}) || !reflect.DeepEqual(zeta.Optional, []string{"c"}) {
		t.Errorf("zeta: %+v", zeta)
	}

	// Die Rückgabe ist eine Kopie: Änderungen wirken nicht auf das Template.
	zeta.Required[0] = "HACKED"
	again, _ := reg.Get("zeta")
	if again.Required[0] != "a" || reg.List()[1].Required[0] != "a" {
		t.Error("Info leaked internal slice")
	}
	names := reg.Names()
	names[0] = "HACKED"
	if reg.Names()[0] != "alpha" {
		t.Error("Names leaked internal slice")
	}
}

func TestTemplateFromOverride(t *testing.T) {
	reg := tplMustLoad(t, map[string]string{"f.tmpl": tplFile(
		"channel: email\nformat: text\nsubject: s\nfrom: \"Support <support@example.com>\"", "x")})
	got, err := reg.Render("f", nil)
	if err != nil || got.From != "Support <support@example.com>" {
		t.Fatalf("From = %q err=%v", got.From, err)
	}
}

func TestTemplateCRLFAndBOM(t *testing.T) {
	// Dateien, die unter Windows gespeichert wurden, müssen genauso funktionieren.
	content := "\ufeff---\r\nchannel: email\r\nformat: text\r\nsubject: s\r\nrequired: [a]\r\n---\r\nHallo {{.a}}\r\n"
	reg := tplMustLoad(t, map[string]string{"win.tmpl": content})
	got, err := reg.Render("win", map[string]any{"a": "Welt"})
	if err != nil || got.Body != "Hallo Welt" {
		t.Fatalf("body = %q err=%v", got.Body, err)
	}
}
