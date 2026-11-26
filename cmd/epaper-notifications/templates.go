package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"epaper-notificaitons/internal/config"
	"epaper-notificaitons/internal/template"
)

const templatesUsage = `Usage:
  epaper-notifications templates check
      Prüft alle Templates (Header, Syntax, Variablen, HTML-Escaping), ohne den Service zu starten.

  epaper-notifications templates render <name> [<json>|-]
      Rendert ein Template mit den angegebenen Daten und gibt Betreff und Body aus.
      Die Daten kommen als JSON-Objekt, "-" liest von stdin.

      epaper-notifications templates render password_reset '{"user_name":"Max","reset_link":"https://example.com/r"}'

Das Template-Verzeichnis kommt aus TEMPLATES_DIR (Default ./templates), die .env wird berücksichtigt.
`

// templatesCommand implementiert "templates check" und "templates render".
// Exit-Codes: 0 = ok, 1 = Fehler im Template oder in den Daten, 2 = falsche Benutzung.
func templatesCommand(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, templatesUsage)
		return 2
	}

	envFile := os.Getenv("ENV_FILE")
	if envFile == "" {
		envFile = ".env"
	}
	if _, err := config.LoadDotEnv(envFile); err != nil {
		fmt.Fprintln(stderr, "epaper-notifications:", err)
		return 1
	}
	dir := os.Getenv("TEMPLATES_DIR")
	if dir == "" {
		dir = "./templates"
	}

	switch args[0] {
	case "check":
		return templatesCheck(dir, stdout, stderr)
	case "render":
		if len(args) < 2 || len(args) > 3 {
			fmt.Fprint(stderr, templatesUsage)
			return 2
		}
		dataArg := ""
		if len(args) == 3 {
			dataArg = args[2]
		}
		return templatesRender(dir, args[1], dataArg, stdin, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], templatesUsage)
		return 2
	}
}

func templatesCheck(dir string, stdout, stderr io.Writer) int {
	reg, err := template.Load(dir)
	if err != nil {
		fmt.Fprintln(stderr, "epaper-notifications:", err)
		return 1
	}
	for _, f := range reg.Ignored() {
		fmt.Fprintf(stderr, "warning: %s is not a template (templates must end in .tmpl) and is ignored\n", f)
	}
	if reg.Len() == 0 {
		fmt.Fprintf(stderr, "epaper-notifications: no templates found in %s (expected *.tmpl files)\n", dir)
		return 1
	}

	fmt.Fprintf(stdout, "OK: %d templates in %s\n\n", reg.Len(), dir)
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tCHANNEL\tFORMAT\tREQUIRED\tOPTIONAL")
	for _, t := range reg.List() {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", t.Name, t.Channel, t.Format, joinOrDash(t.Required), joinOrDash(t.Optional))
	}
	_ = tw.Flush()
	return 0
}

func templatesRender(dir, name, dataArg string, stdin io.Reader, stdout, stderr io.Writer) int {
	reg, err := template.Load(dir)
	if err != nil {
		fmt.Fprintln(stderr, "epaper-notifications:", err)
		return 1
	}

	data := map[string]any{}
	switch {
	case dataArg == "-":
		raw, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintln(stderr, "epaper-notifications: read stdin:", err)
			return 1
		}
		dataArg = string(raw)
		fallthrough
	case dataArg != "":
		if err := json.Unmarshal([]byte(dataArg), &data); err != nil {
			fmt.Fprintln(stderr, "epaper-notifications: data is not a valid JSON object:", err)
			return 1
		}
	}

	out, err := reg.Render(name, data)
	if err != nil {
		var mv *template.MissingVarsError
		switch {
		case errors.Is(err, template.ErrUnknownTemplate):
			fmt.Fprintf(stderr, "epaper-notifications: %v (available: %s)\n", err, strings.Join(reg.Names(), ", "))
		case errors.As(err, &mv):
			fmt.Fprintf(stderr, "epaper-notifications: %v\n", err)
		default:
			fmt.Fprintln(stderr, "epaper-notifications:", err)
		}
		return 1
	}

	from := out.From
	if from == "" {
		from = "(Default des Channels)"
	}
	fmt.Fprintf(stdout, "Channel: %s\nFormat:  %s\nFrom:    %s\nSubject: %s\n\n%s\n", out.Channel, out.Format, from, out.Subject, out.Body)
	return 0
}

func joinOrDash(s []string) string {
	if len(s) == 0 {
		return "-"
	}
	return strings.Join(s, ", ")
}
