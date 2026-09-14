package auth

import (
	"context"
	"testing"
)

// give the account a TOTP secret without the enrolment dance: the query under
// test only cares whether the column holds anything.
func enrol(t *testing.T, s *Service, id string) {
	t.Helper()
	if _, err := s.st.DB.Exec(`UPDATE users SET totp_secret_enc = ? WHERE id = ?`, []byte("secret"), id); err != nil {
		t.Fatal(err)
	}
}

// A server adopted into a fleet gets islet-controller: an admin reached only by
// an API token, with a random password nobody holds. Counting it here made
// "two-factor on every admin account" impossible to satisfy — no sign-in exists
// during which a secret could be enrolled — so a maintainer with 2FA correctly
// on saw a permanent failure with no fix offered anywhere in the panel.
func TestAllAdminsHave2FAIgnoresServiceAccounts(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	human, err := svc.CreateUser(ctx, "maintainer", "correct horse battery staple", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if svc.AllAdminsHave2FA(ctx) {
		t.Fatal("an admin without a second factor should fail the check")
	}

	enrol(t, svc, human.ID)
	if !svc.AllAdminsHave2FA(ctx) {
		t.Fatal("the only admin has 2FA, so the check should pass")
	}

	controller, err := svc.CreateServiceUser(ctx, "islet-controller", "unused random password", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !controller.IsService {
		t.Fatal("CreateServiceUser must mark the account as a service account")
	}
	if !svc.AllAdminsHave2FA(ctx) {
		t.Fatal("a token-only service account must not drag the check down")
	}

	// A second human admin without 2FA must still fail it.
	if _, err := svc.CreateUser(ctx, "colleague", "another long password here", "admin"); err != nil {
		t.Fatal(err)
	}
	if svc.AllAdminsHave2FA(ctx) {
		t.Fatal("a human admin without 2FA must fail the check")
	}
}
