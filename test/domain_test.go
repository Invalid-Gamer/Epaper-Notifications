package test

import (
	"epaper-notificaitons/internal/domain"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestStatusTransitions(t *testing.T) {
	tests := []struct {
		from, to domain.Status
		ok       bool
	}{
		{domain.StatusQueued, domain.StatusSending, true},
		{domain.StatusQueued, domain.StatusFailed, true},
		{domain.StatusQueued, domain.StatusSent, false},
		{domain.StatusSending, domain.StatusSent, true},
		{domain.StatusSending, domain.StatusRetrying, true},
		{domain.StatusSending, domain.StatusFailed, true},
		{domain.StatusRetrying, domain.StatusSending, true},
		{domain.StatusRetrying, domain.StatusSent, false},
		{domain.StatusSent, domain.StatusSending, false},
		{domain.StatusFailed, domain.StatusQueued, false},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s->%s", tt.from, tt.to), func(t *testing.T) {
			if got := tt.from.CanTransitionTo(tt.to); got != tt.ok {
				t.Fatalf("CanTransitionTo = %v, want %v", got, tt.ok)
			}
		})
	}
}

func TestNotificationTransition(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	n := &domain.Notification{Status: domain.StatusQueued, LastError: &domain.ErrorInfo{Code: "x"}}

	if err := n.Transition(domain.StatusSent, now); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("queued->sent should be invalid, got %v", err)
	}
	if err := n.Transition(domain.StatusSending, now); err != nil {
		t.Fatal(err)
	}
	if err := n.Transition(domain.StatusSent, now); err != nil {
		t.Fatal(err)
	}
	if n.SentAt == nil || !n.SentAt.Equal(now) {
		t.Errorf("SentAt not set: %v", n.SentAt)
	}
	if n.LastError != nil {
		t.Error("LastError should be cleared after success")
	}
	if !n.Status.IsTerminal() {
		t.Error("sent must be terminal")
	}
}

func TestIsTemporary(t *testing.T) {
	cause := errors.New("boom")
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"temporary", domain.NewTemporaryError("net", "x", cause), true},
		{"permanent", domain.NewPermanentError("auth", "x", cause), false},
		{"wrapped temporary", fmt.Errorf("ctx: %w", domain.NewTemporaryError("net", "x", cause)), true},
		{"unclassified", cause, false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := domain.IsTemporary(tt.err); got != tt.want {
				t.Fatalf("IsTemporary = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSendErrorUnwrap(t *testing.T) {
	cause := errors.New("boom")
	err := domain.NewPermanentError("smtp_auth_failed", "login rejected", cause)
	if !errors.Is(err, cause) {
		t.Fatal("cause must be reachable via errors.Is")
	}
	if !strings.Contains(err.Error(), "smtp_auth_failed") {
		t.Errorf("Error() = %q", err.Error())
	}
}

func TestNewIDUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := domain.NewID()
		if !strings.HasPrefix(id, "ntf_") || len(id) != 4+24 {
			t.Fatalf("unexpected id %q", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

func TestNormalizeEmail(t *testing.T) {
	if got := domain.NormalizeEmail("  Max@Example.COM "); got != "max@example.com" {
		t.Fatalf("got %q", got)
	}
}
