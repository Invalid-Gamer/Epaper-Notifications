# Templates schreiben

Ein Template ist **eine Datei** in ``. Der Dateiname ohne `.tmpl` ist der Template-Name, den andere Services im Request angeben (`password_reset.tmpl` wird zu `"template": "password_reset"`).

## Aufbau

```
---
description: Link zum Zurücksetzen des Passworts
channel: email
format: html
subject: "Passwort zurücksetzen"
required: [user_name, reset_link]
optional: [expires_in]
---
<p>Hallo {{.user_name}},</p>
<p><a href="{{.reset_link}}">Neues Passwort festlegen</a></p>
{{if .expires_in}}<p>Der Link ist {{.expires_in}} gültig.</p>{{end}}
```

Oben steht ein YAML-Header zwischen zwei `---`-Zeilen, darunter der Body als [Go-Template](https://pkg.go.dev/text/template).

### Header-Felder

| Feld | Pflicht | Bedeutung |
|---|---|---|
| `channel` | ja | Über welchen Channel das Template verschickt wird (`email`). Es muss eine passende Datei `config/channels/<channel>.yaml` geben. |
| `format` | ja | `html` oder `text`. Es gibt bewusst keinen Default, damit man es entscheidet. |
| `subject` | ja | Betreff, ebenfalls ein Template (`"Hallo {{.user_name}}"`). Immer Text, nie HTML. |
| `required` | nein | Variablen, die der Aufrufer angeben muss. Fehlt eine, lehnt die API den Request mit HTTP 422 ab und nennt sie (API ab Phase 5, die Prüfung selbst gibt es schon). |
| `optional` | nein | Variablen, die fehlen dürfen. Sind sie nicht angegeben, sind sie leer. |
| `description` | nein | Kurzbeschreibung, erscheint in `GET /v1/templates` (ab Phase 5). |
| `from` | nein | Eigener Absender nur für dieses Template, z. B. `"Support <support@example.com>"`. Ohne Angabe gilt der Default des Channels (wirksam ab Phase 3). |

Unbekannte Header-Felder sind ein Fehler. So fällt ein Tippfehler wie `subjct:` beim Start auf.

## Variablen

Variablen kommen im Request als JSON-Objekt (`"data": {...}`) und werden im Template mit `{{.name}}` verwendet.

- **Jede verwendete Variable muss in `required` oder `optional` stehen.** Sonst startet der Service nicht und schlägt einen ähnlichen Namen vor (`references .usr_name ... did you mean .user_name?`).
- Als **fehlend** gilt eine Pflichtvariable, wenn sie nicht vorhanden, `null` oder ein leerer bzw. nur aus Leerzeichen bestehender String ist. `0` und `false` zählen als angegeben.
- Zusätzliche, nicht deklarierte Felder im Request werden ignoriert.
- Namen: Buchstaben, Ziffern und `_`, nicht mit einer Ziffer beginnend.

## HTML oder Text

| | `format: html` | `format: text` |
|---|---|---|
| Engine | `html/template` | `text/template` |
| Eingesetzte Werte | werden automatisch escaped (`<` wird `&lt;`) | unverändert |
| Links | `javascript:`-URLs werden entschärft | unverändert |
| Geeignet für | gestaltete Mails mit Buttons | Codes, Alerts, einfache Nachrichten |

Der Betreff ist in beiden Fällen Text. Zeilenumbrüche im gerenderten Betreff werden zu Leerzeichen (Schutz davor, dass ein Wert wie `"Max\r\nBcc: ..."` Mail-Header einschleust).

HTML-Mails sollten Inline-Styles verwenden, weil viele Mail-Clients `<style>`-Blöcke entfernen. Mehrzeilige Nutzertexte (z. B. in `support`) zeigt `white-space: pre-wrap` korrekt an.

## Funktionen

Alle Standardfunktionen von Go-Templates (`if`, `range`, `with`, `and`, `or`, `eq`, `printf`, ...) und zusätzlich:

| Funktion | Beispiel | Wirkung |
|---|---|---|
| `default` | `{{default "Kunde" .user_name}}` | Fallback, wenn der Wert leer ist |

## Ein neues Template anlegen

1. Datei `templates/mein_template.tmpl` anlegen (Name: Kleinbuchstaben, Ziffern, `_`, `-`).
2. Prüfen: `make templates-check`
3. Vorschau mit Beispieldaten:
   ```bash
   make template-render T=mein_template D='{"user_name":"Max"}'
   ```
4. Service neu starten: `make restart` (Templates werden beim Start geladen).

Dateien, die mit `_` oder `.` beginnen, werden übersprungen (so lässt sich ein Template deaktivieren, z. B. `_entwurf.tmpl`). Andere Dateien ohne `.tmpl`-Endung ignoriert der Service mit einer Warnung im Log, damit ein falsch benanntes Template nicht unbemerkt bleibt.

## Was beim Start geprüft wird

Alle Templates werden beim Start vollständig geprüft. **Ein Fehler verhindert den Start**, und es werden alle Probleme auf einmal gemeldet, mit Zeilennummern, die zur Datei passen:

```
epaper-notifications: invalid templates (2 problems):
  - templates/reset.tmpl: body: references .usr_name (reset.tmpl:9:5) but it is not declared in 'required' or 'optional' - did you mean .user_name?
  - templates/old.tmpl: file must start with a header block: the first line must be '---'
```

Geprüft werden Header, Syntax von Betreff und Body, verwendete Variablen, HTML-Escaping (z. B. ein nicht geschlossenes Attribut) und ob der Channel konfiguriert ist.

## Stolperfallen

- **Anführungszeichen im Betreff:** Enthält der Betreff selbst doppelte Anführungszeichen, den YAML-Wert in einfache setzen: `subject: '[{{default "ALERT" .severity}}] {{.title}}'`.
- **Doppelpunkt im Betreff:** Werte mit `: ` immer in Anführungszeichen setzen.
- **Zeile mit nur `---` im Body** wird nicht als Trennzeichen gewertet, nur die ersten beiden `---`-Zeilen der Datei zählen.
- **`range` und `with`:** Innerhalb dieser Blöcke zeigt `.` auf das aktuelle Element. Auf die Eingabedaten greift man dort mit `$.name` zu. Felder des Elements werden nicht gegen `required` und `optional` geprüft.
- **Leerzeilen in Text-Templates:** Mit `{{if ...}}` entstehen leicht doppelte Leerzeilen. `make template-render` zeigt das Ergebnis sofort.

## Noch nicht möglich

Multipart-Mails (HTML mit Text-Fallback), Mehrsprachigkeit und gemeinsame Layout-Bausteine sind nicht Teil dieser Phase. Die Struktur lässt sie später zu.
