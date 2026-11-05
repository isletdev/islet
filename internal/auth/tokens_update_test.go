package auth

import (
	"context"
	"errors"
	"testing"
)

// Changing a token's scopes keeps its secret working, with the new access on
// the next call; someone else's token is out of reach unless you are an admin.
func TestUpdateTokenScopesKeepsTheSecret(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	owner, err := svc.CreateUser(ctx, "owner", "correct horse battery staple", "admin")
	if err != nil {
		t.Fatal(err)
	}
	other, err := svc.CreateUser(ctx, "other", "correct horse battery staple", "deployer")
	if err != nil {
		t.Fatal(err)
	}
	secret, tok, err := svc.CreateToken(ctx, owner.ID, "workspace app", "read,logs,containers,notify", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateTokenScopes(ctx, other.ID, tok.ID, "read,domains", false); !errors.Is(err, ErrBadToken) {
		t.Fatalf("another user changed the token: %v", err)
	}
	if _, err := svc.UpdateTokenScopes(ctx, owner.ID, tok.ID, "read,nonsense", false); err == nil {
		t.Fatal("an unknown scope was accepted")
	}
	if _, err := svc.UpdateTokenScopes(ctx, owner.ID, tok.ID, "read,domains,deploy", false); err != nil {
		t.Fatal(err)
	}
	_, after, err := svc.UserByToken(ctx, secret)
	if err != nil {
		t.Fatalf("the secret stopped working: %v", err)
	}
	if after.Scopes != "read,domains,deploy" || svc.TokenScopes(ctx, tok.ID) != after.Scopes {
		t.Fatalf("scopes = %q", after.Scopes)
	}
	if _, err := svc.UpdateTokenScopes(ctx, other.ID, "missing", "read", true); !errors.Is(err, ErrBadToken) {
		t.Fatalf("a missing token was reported as updated: %v", err)
	}
}
