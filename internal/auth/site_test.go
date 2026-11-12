package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A site person signs in on one host, and what they hold there is worth
// nothing anywhere else: not on another host, not as a panel session, not as a
// gate token, not as an API token.
func TestSitePersonSignsInToOneHostOnly(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	if _, err := svc.CreateSiteUser(ctx, "admin", "Bob@Client.com", "Bob", "correct horse battery", false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.SiteLogin(ctx, "bob@client.com", "wrong password here", "a.example.com", "1.2.3.4", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("a wrong password: %v", err)
	}
	tok, sess, err := svc.SiteLogin(ctx, " BOB@client.com ", "correct horse battery", "A.example.com:443", "1.2.3.4", "")
	if err != nil {
		t.Fatal(err)
	}
	if sess.MFAPending || sess.Host != "a.example.com" {
		t.Fatalf("session = %+v", sess)
	}
	if _, u, err := svc.SiteSessionByToken(ctx, tok, "a.example.com"); err != nil || u.Username != "bob@client.com" {
		t.Fatalf("on its own host: %v", err)
	}
	if _, _, err := svc.SiteSessionByToken(ctx, tok, "b.example.com"); err == nil {
		t.Fatal("a session made on one host opened another")
	}
	if _, err := svc.SessionByToken(ctx, tok); err == nil {
		t.Fatal("a site sign-in is a panel session")
	}
	if _, err := svc.SessionByGate(ctx, tok); err == nil {
		t.Fatal("a site sign-in is a gate token")
	}
	if _, _, err := svc.UserByToken(ctx, tok); err == nil {
		t.Fatal("a site sign-in is an API token")
	}
	// The panel's own sign-in does not know them.
	if _, _, err := svc.Login(ctx, "bob@client.com", "correct horse battery", "1.2.3.4", ""); err == nil {
		t.Fatal("a site person signed in to the panel")
	}
}

// Disabling somebody ends what they hold now, not only what they could start.
func TestDisablingASitePersonEndsTheirSessions(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	u, err := svc.CreateSiteUser(ctx, "admin", "carol", "", "correct horse battery", false)
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := svc.SiteLogin(ctx, "carol", "correct horse battery", "a.example.com", "", "")
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	if _, err := svc.UpdateSiteUser(ctx, "admin", u.ID, SiteUserPatch{Disabled: &yes}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.SiteSessionByToken(ctx, tok, "a.example.com"); err == nil {
		t.Fatal("a disabled person's session still works")
	}
	if _, _, err := svc.SiteLogin(ctx, "carol", "correct horse battery", "a.example.com", "", ""); err == nil {
		t.Fatal("a disabled person signed in")
	}
}

// An invitation sets a password once and then is spent; somebody with no
// password cannot sign in with an empty one.
func TestSiteInvitationIsOneTime(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	u, err := svc.CreateSiteUser(ctx, "admin", "dave", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.SiteLogin(ctx, "dave", "", "a.example.com", "", ""); err == nil {
		t.Fatal("signed in with no password set")
	}
	tok, _, err := svc.SiteInvite(ctx, "admin", u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AcceptSiteInvite(ctx, tok, "short"); err == nil {
		t.Fatal("a weak password was accepted")
	}
	if _, err := svc.AcceptSiteInvite(ctx, tok, "a long enough password"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AcceptSiteInvite(ctx, tok, "another long password"); !errors.Is(err, ErrBadInvite) {
		t.Fatalf("an invitation worked twice: %v", err)
	}
	if _, _, err := svc.SiteLogin(ctx, "dave", "a long enough password", "a.example.com", "", ""); err != nil {
		t.Fatalf("the invited password does not sign in: %v", err)
	}
	// And one that has run out is refused.
	tok2, _, _ := svc.SiteInvite(ctx, "admin", u.ID)
	svc.now = func() time.Time { return time.Now().Add(InviteTTL + time.Minute) }
	if _, err := svc.SiteInviteUser(ctx, tok2); !errors.Is(err, ErrBadInvite) {
		t.Fatalf("an expired invitation: %v", err)
	}
}

// Somebody who must have a second factor is held at the door until they have
// set one up, and then is asked for it every time.
func TestRequiredSecondFactorIsEnrolledThenAsked(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	if _, err := svc.CreateSiteUser(ctx, "admin", "erin", "", "correct horse battery", true); err != nil {
		t.Fatal(err)
	}
	tok, sess, err := svc.SiteLogin(ctx, "erin", "correct horse battery", "a.example.com", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !sess.MFAPending {
		t.Fatal("a required second factor was not asked for")
	}
	sess, u, err := svc.SiteSessionByToken(ctx, tok, "a.example.com")
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := svc.SiteBeginTOTP(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	again, _, _ := svc.SiteBeginTOTP(ctx, u)
	if again != secret {
		t.Fatal("asking for the key again replaced the one already scanned")
	}
	code, _ := totpCode(secret, time.Now().Unix()/totpStep)
	if err := svc.SiteEnrolTOTP(ctx, sess, u, code, ""); err != nil {
		t.Fatal(err)
	}
	if s2, _, _ := svc.SiteSessionByToken(ctx, tok, "a.example.com"); s2 == nil || s2.MFAPending {
		t.Fatal("enrolling did not complete the sign-in")
	}
	_, next, err := svc.SiteLogin(ctx, "erin", "correct horse battery", "a.example.com", "", "")
	if err != nil || !next.MFAPending {
		t.Fatalf("the next sign-in did not ask for a code: %v", err)
	}
}
