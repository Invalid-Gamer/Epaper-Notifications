package domain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Status ist der Lebenszyklus einer Notification.
type Status string

const (
	StatusQueued   Status = "queued"
	StatusSending  Status = "sending"
	StatusRetrying Status = "retrying"
	StatusSent     Status = "sent"
	StatusFailed   Status = "failed"
)

// IsTerminal meldet, ob von diesem Status aus nichts mehr passiert.
func (s Status) IsTerminal() bool { return s == StatusSent || s == StatusFailed }

// allowed beschreibt die erlaubten Übergänge.
var allowed = map[Status][]Status{
	StatusQueued:   {StatusSending, StatusFailed},
	StatusSending:  {StatusSent, StatusRetrying, StatusFailed},
	StatusRetrying: {StatusSending, StatusFailed},
}

// CanTransitionTo prüft, ob ein Wechsel von s nach to erlaubt ist.
func (s Status) CanTransitionTo(to Status) bool {
	for _, t := range allowed[s] {
		if t == to {
			return true
		}
	}
	return false
}

// ErrorInfo ist der zuletzt aufgetretene Fehler, so wie ihn die Status-API zeigt.
// Enthält bewusst keine internen Details (Cause) oder Zugangsdaten.
type ErrorInfo struct {
	Code      string    `json:"code"`
	Message   string    `json:"message"`
	Temporary bool      `json:"temporary"`
	At        time.Time `json:"at"`
}

// Notification ist ein angenommener Versandauftrag.
type Notification struct {
	ID        string
	Channel   string // "email", später "discord", "ntfy", ...
	Template  string
	To        []string
	ReplyTo   string
	Data      map[string]any
	Requester string // Name des PSK, mit dem der Auftrag kam (für Logs)

	Status        Status
	Attempts      int
	LastError     *ErrorInfo
	NextAttemptAt *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
	SentAt        *time.Time
}

// Transition wechselt den Status und aktualisiert UpdatedAt.
// Unerlaubte Übergänge liefern ErrInvalidTransition.
func (n *Notification) Transition(to Status, now time.Time) error {
	if !n.Status.CanTransitionTo(to) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, n.Status, to)
	}
	n.Status = to
	n.UpdatedAt = now
	if to == StatusSent {
		n.SentAt = &now
		n.NextAttemptAt = nil
		n.LastError = nil
	}
	if to == StatusFailed {
		n.NextAttemptAt = nil
	}
	return nil
}

// NewID erzeugt eine zufällige, nicht ratbare ID, z. B. "ntf_9f2c...".
func NewID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand schlägt praktisch nie fehl; wenn doch, ist weiterlaufen unsicher.
		panic("domain: crypto/rand unavailable: " + err.Error())
	}
	return "ntf_" + hex.EncodeToString(b[:])
}

// Confirmation ist ein Bestätigungs-Token für eine E-Mail-Adresse.
// Gespeichert wird nur der Hash des Tokens, nie das Token selbst.
type Confirmation struct {
	TokenHash   string
	Email       string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	ConfirmedAt *time.Time
}

// NormalizeEmail vereinheitlicht Adressen für Vergleiche und Lookups.
func NormalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
