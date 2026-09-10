package auth

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/isletdev/islet/internal/store"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(h, "correct horse battery staple") {
		t.Fatal("valid password rejected")
	}
	if VerifyPassword(h, "wrong horse battery staple") {
		t.Fatal("wrong password accepted")
	}
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("short password accepted")
	}
}

// RFC 6238 test vector: secret "12345678901234567890" (base32 GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ),
// time 59 -> 287082 for SHA1 with 8 digits; the 6-digit value is the last six: 287082.
func TestTOTPVector(t *testing.T) {
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	code, err := totpCode(secret, 59/30)
	if err != nil {
		t.Fatal(err)
	}
	if code != "287082" {
		t.Fatalf("code = %s, want 287082", code)
	}
	if _, ok := VerifyTOTP(secret, "287082", time.Unix(59, 0), 0); !ok {
		t.Fatal("valid code rejected")
	}
	// Same step again must be rejected as replay.
	if _, ok := VerifyTOTP(secret, "287082", time.Unix(59, 0), 1); ok {
		t.Fatal("replayed code accepted")
	}
	if _, ok := VerifyTOTP(secret, "000000", time.Unix(59, 0), 0); ok {
		t.Fatal("wrong code accepted")
	}
}

func newService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "islet.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	keys, err := LoadOrCreateKeys(dir)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := New(st, keys, dir)
	if err != nil {
		t.Fatal(err)
	}
	return svc, dir
}

func TestSetupLoginAndTOTPFlow(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	needs, _ := svc.NeedsSetup(ctx)
	if !needs {
		t.Fatal("fresh install should need setup")
	}
	tok, err := svc.SetupToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteSetup(ctx, "nope", "admin", "a strong password 123"); err != ErrBadSetupToken {
		t.Fatalf("bad token: got %v", err)
	}
	u, err := svc.CompleteSetup(ctx, tok, "Admin", "a strong password 123")
	if err != nil {
		t.Fatal(err)
	}
	if u.Username != "admin" || u.Role != "admin" {
		t.Fatalf("user = %+v", u)
	}
	if _, err := svc.CompleteSetup(ctx, tok, "again", "a strong password 123"); err != ErrSetupDone {
		t.Fatalf("second setup: got %v", err)
	}

	// Login without 2FA opens a ready session.
	if _, _, err := svc.Login(ctx, "admin", "wrong password here", "1.2.3.4", "test"); err != ErrInvalidCredentials {
		t.Fatalf("wrong password: got %v", err)
	}
	token, sess, err := svc.Login(ctx, "admin", "a strong password 123", "1.2.3.4", "test")
	if err != nil {
		t.Fatal(err)
	}
	if sess.MFAPending {
		t.Fatal("session should not be pending without 2FA")
	}
	got, err := svc.SessionByToken(ctx, token)
	if err != nil || got.ID != sess.ID {
		t.Fatalf("session lookup: %v %+v", err, got)
	}

	// Enable 2FA.
	secret, otp, err := svc.BeginTOTPSetup(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if otp == "" {
		t.Fatal("empty otpauth url")
	}
	code, _ := totpCode(secret, time.Now().Unix()/30)
	codes, err := svc.EnableTOTP(ctx, u.ID, code)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != recoveryCodeCount {
		t.Fatalf("recovery codes = %d", len(codes))
	}

	// Now login is pending until the second factor.
	svc.now = func() time.Time { return time.Now().Add(2 * time.Minute) } // move past the enable step
	token2, sess2, err := svc.Login(ctx, "admin", "a strong password 123", "1.2.3.4", "test")
	if err != nil {
		t.Fatal(err)
	}
	if !sess2.MFAPending {
		t.Fatal("session should be MFA pending")
	}
	if err := svc.VerifySecondFactor(ctx, sess2, "123456", "1.2.3.4"); err != ErrBadCode {
		t.Fatalf("bad code: got %v", err)
	}
	code2, _ := totpCode(secret, svc.now().Unix()/30)
	if err := svc.VerifySecondFactor(ctx, sess2, code2, "1.2.3.4"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if s, _ := svc.SessionByToken(ctx, token2); s.MFAPending {
		t.Fatal("session still pending after verify")
	}

	// Recovery code works once.
	svc.now = func() time.Time { return time.Now().Add(4 * time.Minute) }
	_, sess3, _ := svc.Login(ctx, "admin", "a strong password 123", "1.2.3.4", "test")
	if err := svc.VerifySecondFactor(ctx, sess3, codes[0], "1.2.3.4"); err != nil {
		t.Fatalf("recovery code: %v", err)
	}
	_, sess4, _ := svc.Login(ctx, "admin", "a strong password 123", "1.2.3.4", "test")
	if err := svc.VerifySecondFactor(ctx, sess4, codes[0], "1.2.3.4"); err != ErrBadCode {
		t.Fatalf("reused recovery code: got %v", err)
	}
	left, _ := svc.RecoveryCodesLeft(ctx, u.ID)
	if left != recoveryCodeCount-1 {
		t.Fatalf("codes left = %d", left)
	}

	// Rate limit kicks in after 10 failures.
	svc.now = time.Now
	for i := 0; i < 10; i++ {
		_, _, _ = svc.Login(ctx, "admin", "wrong password here", "9.9.9.9", "test")
	}
	if _, _, err := svc.Login(ctx, "admin", "a strong password 123", "9.9.9.9", "test"); err != ErrRateLimited {
		t.Fatalf("rate limit: got %v", err)
	}
}
