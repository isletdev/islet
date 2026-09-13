package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/store"
)

// fleetToken prints an API token another Islet panel can use to manage this
// server, creating the account it belongs to if the server has never been set
// up.
//
// It exists so joining a server needs no setup wizard on that server and no
// password anywhere: the controller runs this once over SSH, keeps the token,
// and that is the whole credential. Revoking it is deleting the token in
// Settings, or deleting the user.
//
// It must be run on the server itself, as root, which the SSH join already is.
func fleetToken(dataDir, name string) error {
	if strings.TrimSpace(name) == "" {
		name = "managed by another Islet panel"
	}
	if abs, err := filepath.Abs(dataDir); err == nil {
		dataDir = abs
	}
	if _, err := os.Stat(dataDir); err != nil {
		return fmt.Errorf("no Islet data directory at %s; install the daemon first", dataDir)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	st, err := store.Open(ctx, filepath.Join(dataDir, "islet.db"))
	if err != nil {
		return fmt.Errorf("open the database: %w", err)
	}
	defer st.Close()

	keys, err := auth.LoadOrCreateKeys(dataDir)
	if err != nil {
		return err
	}
	as, err := auth.New(st, keys, dataDir)
	if err != nil {
		return err
	}

	// The controller gets its own account, so the audit log can say which
	// actions arrived from the panel rather than attributing them to a person.
	const controller = "islet-controller"
	var u *auth.User
	users, err := as.Users(ctx)
	if err != nil {
		return fmt.Errorf("read users: %w", err)
	}
	for i := range users {
		if users[i].Username == controller {
			u = &users[i]
			break
		}
	}
	if u == nil {
		pw, err := randomPassword()
		if err != nil {
			return err
		}
		// The password is never used and never printed: this account is reached
		// only by the token below.
		u, err = as.CreateUser(ctx, controller, pw, "admin")
		if err != nil {
			return fmt.Errorf("create the controller account: %w", err)
		}
	}
	if u == nil {
		return errors.New("could not find or create the controller account")
	}

	plain, _, err := as.CreateToken(ctx, u.ID, name, "*", 0)
	if err != nil {
		return fmt.Errorf("create the token: %w", err)
	}
	fmt.Println(plain)
	return nil
}

// randomPassword is never shown to anyone: the controller account is reached
// only by the token this command prints.
func randomPassword() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
