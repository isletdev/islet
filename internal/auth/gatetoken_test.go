package auth

import (
	"context"
	"testing"
	"time"
)

// The gate credential and the session are two different values on purpose:
// the first travels to every protected site, the second never leaves the
// panel's own host. Each must open only its own door.
func TestGateTokenIsNotTheSession(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	tok, err := svc.SetupToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteSetup(ctx, tok, "admin", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	session, sess, err := svc.Login(ctx, "admin", "correct horse battery", "1.2.3.4", "test")
	if err != nil {
		t.Fatal(err)
	}
	if sess.GateToken == "" || sess.GateToken == session {
		t.Fatalf("gate token is missing or is the session token: %q", sess.GateToken)
	}

	// The gate value is worthless as a session...
	if _, err := svc.SessionByToken(ctx, sess.GateToken); err == nil {
		t.Fatal("the gate token was accepted as a panel session")
	}
	// ...and the session is not accepted at the gate.
	if _, err := svc.SessionByGate(ctx, session); err == nil {
		t.Fatal("the session token was accepted as a gate token")
	}
	got, err := svc.SessionByGate(ctx, sess.GateToken)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != sess.ID {
		t.Fatalf("gate token resolved to %s, want %s", got.ID, sess.ID)
	}

	// Signing out takes both with it: one row, one lifetime.
	if err := svc.Logout(ctx, session); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SessionByGate(ctx, sess.GateToken); err == nil {
		t.Fatal("the gate token outlived the session it belongs to")
	}
}

// A session waiting on a second factor opens nothing. It is the one case where
// a token exists and the person is not yet signed in.
func TestNoGateTokenUntilTheSecondFactorIsIn(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	tok, _ := svc.SetupToken()
	u, err := svc.CompleteSetup(ctx, tok, "admin", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := svc.BeginTOTPSetup(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := totpCode(secret, time.Now().Unix()/30)
	if _, err := svc.EnableTOTP(ctx, u.ID, code); err != nil {
		t.Fatal(err)
	}
	svc.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	_, sess, err := svc.Login(ctx, "admin", "correct horse battery", "1.2.3.4", "test")
	if err != nil {
		t.Fatal(err)
	}
	if !sess.MFAPending {
		t.Fatal("a user with TOTP should be left pending")
	}
	if sess.GateToken != "" || sess.HasGate {
		t.Fatal("a half-signed-in session was given a gate token")
	}
	// And EnsureGate refuses it too, so no other path can hand one out.
	gate, err := svc.EnsureGate(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SessionByGate(ctx, gate); err == nil {
		t.Fatal("a pending session was given a working gate token")
	}
}
