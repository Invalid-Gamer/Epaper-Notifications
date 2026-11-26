package test

import (
	"epaper-notificaitons/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

func lookupMap(m map[string]string) config.Lookup {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

// Testziel, ähnlich wie die spätere Email-Config.
type testAccount struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	TLS      string `yaml:"tls"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	From     string `yaml:"from"`
	Timeout  string `yaml:"timeout"`
}
type testEmailCfg struct {
	DefaultAccount string                 `yaml:"default_account"`
	Accounts       map[string]testAccount `yaml:"accounts"`
}

func dirWith(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := writeFile(filepath.Join(dir, name), content); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const emailYAML = `
default_account: main
accounts:
  main:
    host: ${SMTP_HOST}
    port: ${SMTP_PORT:-587}
    tls: ${SMTP_TLS:-starttls}
    username: ${SMTP_USER:-}
    password: ${SMTP_PASSWORD:-}
    from: "DoorSign <noreply@example.com>"
`

func TestLoadChannelConfigsExpansion(t *testing.T) {
	dir := dirWith(t, map[string]string{"email.yaml": emailYAML})
	cfgs, err := config.LoadChannelConfigs(dir, lookupMap(map[string]string{
		"SMTP_HOST":     "mail.example.com",
		"SMTP_PASSWORD": "p@ss: w#rd 'quoted' \"dq\"", // Sonderzeichen dürfen das YAML nicht zerstören
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfgs) != 1 || cfgs[0].Name != "email" {
		t.Fatalf("configs: %+v", cfgs)
	}

	var got testEmailCfg
	if err := cfgs[0].Decode(&got); err != nil {
		t.Fatal(err)
	}
	acc := got.Accounts["main"]
	if acc.Host != "mail.example.com" {
		t.Errorf("host = %q", acc.Host)
	}
	if acc.Port != 587 { // Default, und als Zahl erkannt
		t.Errorf("port = %d", acc.Port)
	}
	if acc.TLS != "starttls" || acc.Username != "" {
		t.Errorf("tls=%q user=%q", acc.TLS, acc.Username)
	}
	if acc.Password != "p@ss: w#rd 'quoted' \"dq\"" {
		t.Errorf("password mangled: %q", acc.Password)
	}
	if acc.From != "DoorSign <noreply@example.com>" {
		t.Errorf("from = %q", acc.From)
	}
}

func TestPortFromEnvBecomesNumber(t *testing.T) {
	dir := dirWith(t, map[string]string{"email.yaml": emailYAML})
	cfgs, err := config.LoadChannelConfigs(dir, lookupMap(map[string]string{"SMTP_HOST": "h", "SMTP_PORT": "2525"}))
	if err != nil {
		t.Fatal(err)
	}
	var got testEmailCfg
	if err := cfgs[0].Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Accounts["main"].Port != 2525 {
		t.Errorf("port = %d", got.Accounts["main"].Port)
	}
}

func TestMissingEnvVarIsReportedWithPath(t *testing.T) {
	dir := dirWith(t, map[string]string{"email.yaml": emailYAML})
	_, err := config.LoadChannelConfigs(dir, lookupMap(nil)) // SMTP_HOST fehlt
	ps := problems(t, err)
	if len(ps) != 1 {
		t.Fatalf("problems: %v", ps)
	}
	for _, want := range []string{"email.yaml", "accounts.main.host", "SMTP_HOST", "not set"} {
		if !strings.Contains(ps[0], want) {
			t.Errorf("problem %q lacks %q", ps[0], want)
		}
	}
}

func TestEmptyEnvVarWithoutDefaultIsAllowed(t *testing.T) {
	dir := dirWith(t, map[string]string{"x.yaml": "token: ${TOK}\n"})
	cfgs, err := config.LoadChannelConfigs(dir, lookupMap(map[string]string{"TOK": ""}))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Token string `yaml:"token"`
	}
	if err := cfgs[0].Decode(&v); err != nil || v.Token != "" {
		t.Fatalf("v=%+v err=%v", v, err)
	}
}

func TestEscapeAndKeysAndSequences(t *testing.T) {
	dir := dirWith(t, map[string]string{"x.yaml": `
literal: "$${NOT_EXPANDED}"
mixed: "prefix-${A}-${B:-b}-suffix"
list:
  - ${A}
  - "static"
`})
	cfgs, err := config.LoadChannelConfigs(dir, lookupMap(map[string]string{"A": "1"}))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Literal string   `yaml:"literal"`
		Mixed   string   `yaml:"mixed"`
		List    []string `yaml:"list"`
	}
	if err := cfgs[0].Decode(&v); err != nil {
		t.Fatal(err)
	}
	if v.Literal != "${NOT_EXPANDED}" || v.Mixed != "prefix-1-b-suffix" || len(v.List) != 2 || v.List[0] != "1" {
		t.Errorf("%+v", v)
	}
}

func TestDecodeIsStrict(t *testing.T) {
	dir := dirWith(t, map[string]string{"x.yaml": "hots: typo\n"})
	cfgs, err := config.LoadChannelConfigs(dir, lookupMap(nil))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Host string `yaml:"host"`
	}
	err = cfgs[0].Decode(&v)
	if err == nil || !strings.Contains(err.Error(), "hots") {
		t.Fatalf("unknown field not rejected: %v", err)
	}
}

func TestFileSelection(t *testing.T) {
	dir := dirWith(t, map[string]string{
		"email.yaml":    "a: 1\n",
		"ntfy.yml":      "a: 1\n",
		"_discord.yaml": "a: 1\n", // deaktiviert
		".hidden.yaml":  "a: 1\n",
		"README.md":     "# nope\n",
		"notes.txt":     "nope\n",
	})
	if err := os.Mkdir(filepath.Join(dir, "sub.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfgs, err := config.LoadChannelConfigs(dir, lookupMap(nil))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range cfgs {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "email,ntfy" {
		t.Errorf("names = %v", names)
	}
}

func TestChannelFileProblems(t *testing.T) {
	dir := dirWith(t, map[string]string{
		"email.yaml":    "a: 1\n",
		"email.yml":     "a: 2\n", // Duplikat
		"Bad Name.yaml": "a: 1\n", // ungültiger Name
		"empty.yaml":    "# nur Kommentar\n",
		"broken.yaml":   "a: [unclosed\n",
	})
	_, err := config.LoadChannelConfigs(dir, lookupMap(nil))
	ps := problems(t, err)
	for _, want := range []string{"already defined", "lowercase channel name", "file is empty", "invalid YAML"} {
		if !hasProblem(ps, want) {
			t.Errorf("missing %q in %v", want, ps)
		}
	}
}

func TestEmptyAndMissingDir(t *testing.T) {
	cfgs, err := config.LoadChannelConfigs(t.TempDir(), lookupMap(nil))
	if err != nil || len(cfgs) != 0 {
		t.Fatalf("cfgs=%v err=%v", cfgs, err)
	}
	if _, err := config.LoadChannelConfigs("/definitely/not/here", lookupMap(nil)); err == nil {
		t.Fatal("expected error for missing dir")
	}
}

// Stellt sicher, dass die mitgelieferte config/channels/email.yaml gültig ist
// und mit den Werten der .env.example geladen werden kann.
func TestShippedEmailYAML(t *testing.T) {
	cfgs, err := config.LoadChannelConfigs("../../config/channels", lookupMap(map[string]string{
		"SMTP_HOST": "mailpit",
		"SMTP_PORT": "1025",
		"SMTP_TLS":  "none",
	}))
	if err != nil {
		t.Fatal(err)
	}
	var email *config.ChannelConfig
	for i := range cfgs {
		if cfgs[i].Name == "email" {
			email = &cfgs[i]
		}
	}
	if email == nil {
		t.Fatal("config/channels/email.yaml not found")
	}

	var got testEmailCfg
	if err := email.Decode(&got); err != nil {
		t.Fatal(err)
	}
	acc, ok := got.Accounts[got.DefaultAccount]
	if !ok {
		t.Fatalf("default_account %q not defined in accounts", got.DefaultAccount)
	}
	if acc.Host != "mailpit" || acc.Port != 1025 || acc.TLS != "none" {
		t.Errorf("unexpected account: %+v", acc)
	}
	if !strings.Contains(acc.From, "noreply@example.com") {
		t.Errorf("from default not applied: %q", acc.From)
	}
}
