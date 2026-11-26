// Package memory ist die In-Memory-Implementierung der Repositories.
// Alle Daten gehen beim Neustart verloren. Das ist vorerst gewollt, bis der
// zentrale DB-Service steht.
package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	"epaper-notificaitons/internal/domain"
	"epaper-notificaitons/internal/store"
)

// New erzeugt einen leeren Store mit beiden Repositories.
func New() store.Store {
	return store.Store{
		Notifications: &notifications{items: make(map[string]domain.Notification)},
		Confirmations: &confirmations{items: make(map[string]domain.Confirmation)},
	}
}

// ---------------------------------------------------------------- Notifications

type notifications struct {
	mu    sync.RWMutex
	items map[string]domain.Notification
}

var _ store.NotificationRepository = (*notifications)(nil)

func (r *notifications) Create(ctx context.Context, n domain.Notification) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.items[n.ID]; exists {
		return domain.ErrAlreadyExists
	}
	r.items[n.ID] = cloneNotification(n)
	return nil
}

func (r *notifications) Get(ctx context.Context, id string) (domain.Notification, error) {
	if err := ctx.Err(); err != nil {
		return domain.Notification{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	n, ok := r.items[id]
	if !ok {
		return domain.Notification{}, domain.ErrNotFound
	}
	return cloneNotification(n), nil
}

func (r *notifications) Update(ctx context.Context, n domain.Notification) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[n.ID]; !ok {
		return domain.ErrNotFound
	}
	r.items[n.ID] = cloneNotification(n)
	return nil
}

// ---------------------------------------------------------------- Confirmations

type confirmations struct {
	mu    sync.Mutex
	items map[string]domain.Confirmation // Key: TokenHash
}

var _ store.ConfirmationRepository = (*confirmations)(nil)

func (r *confirmations) Create(ctx context.Context, c domain.Confirmation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.Email = domain.NormalizeEmail(c.Email)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.items[c.TokenHash]; exists {
		return domain.ErrAlreadyExists
	}
	r.items[c.TokenHash] = cloneConfirmation(c)
	return nil
}

func (r *confirmations) Confirm(ctx context.Context, tokenHash string, now time.Time) (domain.Confirmation, error) {
	if err := ctx.Err(); err != nil {
		return domain.Confirmation{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.items[tokenHash]
	if !ok {
		return domain.Confirmation{}, domain.ErrTokenNotFound
	}
	if c.ConfirmedAt != nil {
		return domain.Confirmation{}, domain.ErrTokenUsed
	}
	if now.After(c.ExpiresAt) {
		return domain.Confirmation{}, domain.ErrTokenExpired
	}
	c.ConfirmedAt = &now
	r.items[tokenHash] = cloneConfirmation(c)
	return cloneConfirmation(c), nil
}

func (r *confirmations) ListByEmail(ctx context.Context, email string) ([]domain.Confirmation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	email = domain.NormalizeEmail(email)
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.Confirmation
	for _, c := range r.items {
		if c.Email == email {
			out = append(out, cloneConfirmation(c))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// ---------------------------------------------------------------- Kopien
// Der Store gibt nie interne Zeiger heraus, damit Aufrufer ihn nicht
// versehentlich (und ohne Lock) verändern.

func cloneNotification(n domain.Notification) domain.Notification {
	n.To = append([]string(nil), n.To...)
	n.Data = cloneMap(n.Data)
	if n.LastError != nil {
		e := *n.LastError
		n.LastError = &e
	}
	n.NextAttemptAt = cloneTime(n.NextAttemptAt)
	n.SentAt = cloneTime(n.SentAt)
	return n
}

func cloneConfirmation(c domain.Confirmation) domain.Confirmation {
	c.ConfirmedAt = cloneTime(c.ConfirmedAt)
	return c
}

func cloneTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	c := *t
	return &c
}

func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = cloneValue(v)
	}
	return out
}

func cloneValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return cloneMap(t)
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = cloneValue(t[i])
		}
		return out
	default:
		return v // Skalare (string, float64, bool, nil) sind unveränderlich
	}
}
