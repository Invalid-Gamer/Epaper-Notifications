package test

import (
	"context"
	"epaper-notificaitons/internal/store/memory"
	"errors"
	"sync"
	"testing"
	"time"

	"epaper-notificaitons/internal/domain"
)

var ctx = context.Background()

func sample(id string) domain.Notification {
	return domain.Notification{
		ID:       id,
		Channel:  "email",
		Template: "password_reset",
		To:       []string{"user@example.com"},
		Data:     map[string]any{"user_name": "Max", "nested": map[string]any{"a": "b"}},
		Status:   domain.StatusQueued,
	}
}

func TestNotificationCreateGetUpdate(t *testing.T) {
	repo := memory.New().Notifications

	if _, err := repo.Get(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get unknown: %v", err)
	}
	if err := repo.Create(ctx, sample("a")); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, sample("a")); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate Create: %v", err)
	}

	n, err := repo.Get(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	n.Status = domain.StatusSent
	if err := repo.Update(ctx, n); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.Get(ctx, "a")
	if got.Status != domain.StatusSent {
		t.Errorf("status = %s", got.Status)
	}

	if err := repo.Update(ctx, sample("missing")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Update unknown: %v", err)
	}
}

func TestNotificationIsolation(t *testing.T) {
	repo := memory.New().Notifications
	orig := sample("a")
	if err := repo.Create(ctx, orig); err != nil {
		t.Fatal(err)
	}

	// Veränderung am Original nach Create darf den Store nicht beeinflussen.
	orig.To[0] = "evil@example.com"
	orig.Data["nested"].(map[string]any)["a"] = "changed"

	got, _ := repo.Get(ctx, "a")
	if got.To[0] != "user@example.com" {
		t.Errorf("To leaked: %v", got.To)
	}
	if got.Data["nested"].(map[string]any)["a"] != "b" {
		t.Errorf("Data leaked: %v", got.Data)
	}

	// Veränderung an einer gelesenen Kopie ebenfalls nicht.
	got.To[0] = "evil2@example.com"
	again, _ := repo.Get(ctx, "a")
	if again.To[0] != "user@example.com" {
		t.Errorf("read copy leaked: %v", again.To)
	}
}

func TestContextCancelled(t *testing.T) {
	repo := memory.New().Notifications
	c, cancel := context.WithCancel(ctx)
	cancel()
	if err := repo.Create(c, sample("a")); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestConfirmationLifecycle(t *testing.T) {
	repo := memory.New().Confirmations
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)

	c := domain.Confirmation{
		TokenHash: "h1", Email: "  User@Example.com ",
		CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	}
	if err := repo.Create(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, c); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate: %v", err)
	}

	got, err := repo.Confirm(ctx, "h1", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got.ConfirmedAt == nil || got.Email != "user@example.com" {
		t.Errorf("unexpected: %+v", got)
	}

	// Zweites Einlösen
	if _, err := repo.Confirm(ctx, "h1", now.Add(2*time.Hour)); !errors.Is(err, domain.ErrTokenUsed) {
		t.Fatalf("second confirm: %v", err)
	}
	// Unbekannt
	if _, err := repo.Confirm(ctx, "zzz", now); !errors.Is(err, domain.ErrTokenNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestConfirmationExpired(t *testing.T) {
	repo := memory.New().Confirmations
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	_ = repo.Create(ctx, domain.Confirmation{TokenHash: "h", Email: "a@b.de", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})

	if _, err := repo.Confirm(ctx, "h", now.Add(2*time.Hour)); !errors.Is(err, domain.ErrTokenExpired) {
		t.Fatalf("got %v", err)
	}
	// Abgelaufenes Token wurde nicht als bestätigt markiert.
	list, _ := repo.ListByEmail(ctx, "a@b.de")
	if len(list) != 1 || list[0].ConfirmedAt != nil {
		t.Fatalf("unexpected list: %+v", list)
	}
}

func TestConfirmOnlyOnceUnderConcurrency(t *testing.T) {
	repo := memory.New().Confirmations
	now := time.Now()
	_ = repo.Create(ctx, domain.Confirmation{TokenHash: "h", Email: "a@b.de", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})

	const workers = 50
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		success int
	)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := repo.Confirm(ctx, "h", now); err == nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if success != 1 {
		t.Fatalf("token redeemed %d times, want exactly 1", success)
	}
}

func TestListByEmailOrderAndNormalization(t *testing.T) {
	repo := memory.New().Confirmations
	base := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	for i, h := range []string{"old", "new"} {
		_ = repo.Create(ctx, domain.Confirmation{
			TokenHash: h, Email: "Max@Example.com",
			CreatedAt: base.Add(time.Duration(i) * time.Hour), ExpiresAt: base.Add(48 * time.Hour),
		})
	}
	_ = repo.Create(ctx, domain.Confirmation{TokenHash: "other", Email: "x@y.de", CreatedAt: base, ExpiresAt: base})

	list, err := repo.ListByEmail(ctx, " MAX@example.com ")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].TokenHash != "new" {
		t.Fatalf("unexpected: %+v", list)
	}
}
