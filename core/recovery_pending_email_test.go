package core

import "testing"

func TestSetPendingEmail_AutoCreatesRecordOnFirstRequest(t *testing.T) {
	s := NewRecoveryStore()

	s.SetPendingEmail("addr-1", "a@example.com", "123456", 1000)

	r, ok := s.Get("addr-1")
	if !ok {
		t.Fatalf("expected a record to be auto-created")
	}
	if r.PendingEmail != "a@example.com" || r.PendingEmailToken != "123456" || r.PendingEmailTokenExpiresAt != 1000 {
		t.Fatalf("expected pending fields set, got %+v", r)
	}
	if r.EmailVerified {
		t.Fatalf("expected EmailVerified to stay false until confirmed, got true")
	}
}

func TestSetPendingEmail_ResendOverwritesPreviousPending(t *testing.T) {
	s := NewRecoveryStore()

	s.SetPendingEmail("addr-1", "a@example.com", "111111", 1000)
	s.SetPendingEmail("addr-1", "b@example.com", "222222", 2000)

	r, _ := s.Get("addr-1")
	if r.PendingEmail != "b@example.com" || r.PendingEmailToken != "222222" || r.PendingEmailTokenExpiresAt != 2000 {
		t.Fatalf("expected the SECOND request to fully overwrite the first, got %+v", r)
	}

	// The OLD code must no longer confirm anything - proves a resend
	// genuinely invalidates the earlier code, not just cosmetically.
	if s.ConfirmPendingEmail("addr-1", "111111", 1500) {
		t.Fatalf("expected the stale first code to no longer confirm after a resend")
	}
}

func TestConfirmPendingEmail_SucceedsWithCorrectCodeBeforeExpiry(t *testing.T) {
	s := NewRecoveryStore()
	s.SetPendingEmail("addr-1", "a@example.com", "123456", 1000)

	if !s.ConfirmPendingEmail("addr-1", "123456", 999) {
		t.Fatalf("expected confirmation to succeed with the correct code before expiry")
	}

	r, _ := s.Get("addr-1")
	if !r.EmailVerified || r.Email != "a@example.com" {
		t.Fatalf("expected Email/EmailVerified promoted, got %+v", r)
	}
	if r.PendingEmail != "" || r.PendingEmailToken != "" || r.PendingEmailTokenExpiresAt != 0 {
		t.Fatalf("expected pending fields cleared after a successful confirm, got %+v", r)
	}
}

func TestConfirmPendingEmail_RejectsWrongCode(t *testing.T) {
	s := NewRecoveryStore()
	s.SetPendingEmail("addr-1", "a@example.com", "123456", 1000)

	if s.ConfirmPendingEmail("addr-1", "000000", 999) {
		t.Fatalf("expected confirmation to fail with the wrong code")
	}
	r, _ := s.Get("addr-1")
	if r.EmailVerified {
		t.Fatalf("expected EmailVerified to stay false after a wrong-code attempt")
	}
}

func TestConfirmPendingEmail_RejectsExpiredCode(t *testing.T) {
	s := NewRecoveryStore()
	s.SetPendingEmail("addr-1", "a@example.com", "123456", 1000)

	if s.ConfirmPendingEmail("addr-1", "123456", 1001) {
		t.Fatalf("expected confirmation to fail once past the expiry timestamp")
	}
}

func TestConfirmPendingEmail_RejectsWhenNoRequestOutstanding(t *testing.T) {
	s := NewRecoveryStore()

	if s.ConfirmPendingEmail("never-requested", "123456", 999) {
		t.Fatalf("expected confirmation to fail for an address with no pending request at all")
	}
}

func TestConfirmPendingEmail_DoesNotDisturbAnAlreadyVerifiedDifferentEmail(t *testing.T) {
	s := NewRecoveryStore()
	s.SetEmail("addr-1", "old-verified@example.com")

	// A NEW pending request (e.g. "update my email") must not touch the
	// already-verified old one until it's actually confirmed.
	s.SetPendingEmail("addr-1", "new@example.com", "123456", 1000)
	r, _ := s.Get("addr-1")
	if r.Email != "old-verified@example.com" || !r.EmailVerified {
		t.Fatalf("expected the old verified email to remain in effect while a new one is only pending, got %+v", r)
	}

	s.ConfirmPendingEmail("addr-1", "123456", 999)
	r, _ = s.Get("addr-1")
	if r.Email != "new@example.com" {
		t.Fatalf("expected the new email to take over once confirmed, got %+v", r)
	}
}
