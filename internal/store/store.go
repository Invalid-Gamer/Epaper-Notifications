// Package store definiert die Persistenz-Schnittstellen des Services.
//
// Der Rest des Services kennt nur diese Interfaces. Die Persistenz liegt später
// vermutlich in einem zentralen DB-Service. Dann wird ein weiterer Adapter neben
// memory/ angelegt, ohne dass sich Dispatcher, API oder Bestätigung ändern.
package store

import (
	"context"
	"time"

	"epaper-notificaitons/internal/domain"
)

// NotificationRepository speichert Versandaufträge und ihren Status.
type NotificationRepository interface {
	// Create legt eine neue Notification an. Existiert die ID schon,
	// kommt domain.ErrAlreadyExists zurück.
	Create(ctx context.Context, n domain.Notification) error

	// Get liefert eine Kopie der Notification oder domain.ErrNotFound.
	Get(ctx context.Context, id string) (domain.Notification, error)

	// Update ersetzt eine bestehende Notification komplett
	// oder liefert domain.ErrNotFound.
	Update(ctx context.Context, n domain.Notification) error
}

// ConfirmationRepository speichert Bestätigungs-Tokens (nur als Hash).
type ConfirmationRepository interface {
	// Create legt ein neues Token an. Existiert der Hash schon,
	// kommt domain.ErrAlreadyExists zurück.
	Create(ctx context.Context, c domain.Confirmation) error

	// Confirm löst ein Token atomar ein: Es wird genau einmal gültig.
	// Fehler: domain.ErrTokenNotFound, domain.ErrTokenUsed, domain.ErrTokenExpired.
	Confirm(ctx context.Context, tokenHash string, now time.Time) (domain.Confirmation, error)

	// ListByEmail liefert alle Tokens einer Adresse (bestätigte und offene),
	// neueste zuerst. Die Adresse wird normalisiert verglichen.
	ListByEmail(ctx context.Context, email string) ([]domain.Confirmation, error)
}

// Store bündelt alle Repositories, damit sie gemeinsam durchgereicht werden können.
type Store struct {
	Notifications NotificationRepository
	Confirmations ConfirmationRepository
}
