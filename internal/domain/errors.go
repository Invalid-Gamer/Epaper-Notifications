// Package domain enthält die zentralen Typen des Services. Es hat keine
// Abhängigkeiten zu anderen internen Paketen, damit jede Schicht es nutzen kann.
package domain

import (
	"errors"
	"fmt"
)

// Sentinel-Fehler. Mit errors.Is prüfbar, die API mappt sie später auf HTTP-Status.
var (
	ErrNotFound          = errors.New("not found")
	ErrAlreadyExists     = errors.New("already exists")
	ErrQueueFull         = errors.New("queue is full")
	ErrInvalidTransition = errors.New("invalid status transition")

	// Bestätigungs-Tokens
	ErrTokenNotFound = errors.New("confirmation token not found")
	ErrTokenExpired  = errors.New("confirmation token expired")
	ErrTokenUsed     = errors.New("confirmation token already used")
)

// SendError beschreibt einen fehlgeschlagenen Versandversuch eines Channels.
//
// Temporary entscheidet, ob der Dispatcher es erneut versucht (Netzwerkfehler,
// SMTP 4xx) oder die Notification sofort als failed markiert (SMTP 5xx,
// falsche Zugangsdaten, ungültige Adresse).
type SendError struct {
	Temporary bool
	Code      string // maschinenlesbar, z. B. "smtp_auth_failed", "connection_refused"
	Message   string // menschenlesbar, darf in der API-Antwort erscheinen
	Cause     error  // ursprünglicher Fehler, nur fürs Log
}

func (e *SendError) Error() string {
	kind := "permanent"
	if e.Temporary {
		kind = "temporary"
	}
	if e.Cause != nil {
		return fmt.Sprintf("send failed (%s, %s): %s: %v", kind, e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("send failed (%s, %s): %s", kind, e.Code, e.Message)
}

func (e *SendError) Unwrap() error { return e.Cause }

// NewTemporaryError erzeugt einen Fehler, der wiederholt werden darf.
func NewTemporaryError(code, message string, cause error) *SendError {
	return &SendError{Temporary: true, Code: code, Message: message, Cause: cause}
}

// NewPermanentError erzeugt einen Fehler, bei dem ein Retry sinnlos ist.
func NewPermanentError(code, message string, cause error) *SendError {
	return &SendError{Temporary: false, Code: code, Message: message, Cause: cause}
}

// IsTemporary meldet true, wenn err (oder ein eingewickelter Fehler) ein
// temporärer SendError ist. Nicht klassifizierte Fehler gelten bewusst als
// permanent: Lieber eine Mail als fehlgeschlagen melden, als sie durch
// blindes Wiederholen doppelt zuzustellen.
func IsTemporary(err error) bool {
	var se *SendError
	return errors.As(err, &se) && se.Temporary
}
