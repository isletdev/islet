// Package ai keeps the models a server has been set up to use.
//
// A provider is a thing somebody configured once and named: a Claude
// subscription, an Anthropic key, an OpenAI-compatible endpoint. The assistant
// picks one per conversation and a workspace agent picks one when it is
// created, which is the whole reason this is a table rather than a setting —
// "which model" is a question with more than one answer on the same server, and
// the answer belongs to the conversation rather than to the machine.
//
// Credentials arrive here already sealed and leave the same way. The daemon's
// keys live in the API layer with every other secret, and a store that cannot
// decrypt what it holds is a store that cannot leak it by accident.
package ai

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/isletdev/islet/internal/store"
)

// Kinds are the ways a model can be reached.
const (
	// KindAnthropic and KindOpenAI are API keys billed per token.
	KindAnthropic = "anthropic"
	KindOpenAI    = "openai"
	// KindSubscription spends a Claude subscription through Claude Code on
	// this server, and has no key of its own — the credentials belong to
	// whoever signed in with the CLI.
	KindSubscription = "subscription"
)

// Provider is one configured model.
type Provider struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Model   string `json:"model"`
	BaseURL string `json:"baseUrl"`
	// Command and MCPConfig belong to the subscription kind: where Claude Code
	// is, and the file that hands it Islet's tools.
	Command   string `json:"command"`
	MCPConfig string `json:"mcpConfig"`
	Default   bool   `json:"default"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`

	// SealedKey never leaves the daemon. It is the API layer's business to
	// decrypt, and nothing here can.
	SealedKey string `json:"-"`
	// KeySet is what the panel needs instead: whether a credential exists, not
	// what it is.
	KeySet bool `json:"keySet"`
}

// Service reads and writes providers.
type Service struct{ st *store.Store }

func New(st *store.Store) *Service { return &Service{st: st} }

const cols = `id, name, kind, model, base_url, api_key, command, mcp_config, is_default, created_at, updated_at`

func scan(sc interface{ Scan(...any) error }) (*Provider, error) {
	var p Provider
	var def int
	if err := sc.Scan(&p.ID, &p.Name, &p.Kind, &p.Model, &p.BaseURL, &p.SealedKey, &p.Command, &p.MCPConfig, &def, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	p.Default, p.KeySet = def == 1, p.SealedKey != ""
	return &p, nil
}

// List returns every provider, the default first and then by name, which is the
// order a picker wants.
func (s *Service) List(ctx context.Context) ([]Provider, error) {
	rows, err := s.st.DB.QueryContext(ctx,
		`SELECT `+cols+` FROM ai_providers WHERE server_id = ? ORDER BY is_default DESC, name`, s.st.ServerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Provider{}
	for rows.Next() {
		p, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// Get returns one provider by id.
func (s *Service) Get(ctx context.Context, id string) (*Provider, error) {
	return scan(s.st.DB.QueryRowContext(ctx,
		`SELECT `+cols+` FROM ai_providers WHERE id = ? AND server_id = ?`, id, s.st.ServerID))
}

// Resolve answers "which model is this conversation using".
//
// An id that names a provider wins. Anything else — an empty id, or one whose
// provider has since been deleted — falls back to the default, because a chat
// that was started against a provider somebody later removed should keep
// working rather than become unopenable. It returns nil, nil when the server
// has no providers at all, which is a state the panel has to draw anyway.
func (s *Service) Resolve(ctx context.Context, id string) (*Provider, error) {
	if strings.TrimSpace(id) != "" {
		if p, err := s.Get(ctx, id); err == nil {
			return p, nil
		}
	}
	list, err := s.List(ctx)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return &list[0], nil // List puts the default first
}

// Validate normalises a provider and says what is wrong with it.
func (p *Provider) Validate() error {
	p.Name = strings.TrimSpace(p.Name)
	p.Model = strings.TrimSpace(p.Model)
	p.BaseURL = strings.TrimSpace(p.BaseURL)
	p.Command = strings.TrimSpace(p.Command)
	if p.Name == "" {
		return errors.New("give it a name you will recognise later")
	}
	if len(p.Name) > 60 {
		return errors.New("that name is too long")
	}
	switch p.Kind {
	case KindAnthropic, KindOpenAI, KindSubscription:
	default:
		return fmt.Errorf("unknown kind %q", p.Kind)
	}
	if p.BaseURL != "" && !strings.HasPrefix(p.BaseURL, "http://") && !strings.HasPrefix(p.BaseURL, "https://") {
		return errors.New("the base URL has to start with http:// or https://")
	}
	return nil
}

// Save inserts or updates. The sealed key is left alone when empty, so saving
// a model does not wipe a credential the panel never showed anybody.
func (s *Service) Save(ctx context.Context, p *Provider) error {
	if err := p.Validate(); err != nil {
		return err
	}
	def := 0
	if p.Default {
		def = 1
	}
	if p.ID == "" {
		p.ID = newID()
		// The first provider on a server is the default whether or not
		// anybody said so: with one configured model, nothing should have to
		// be chosen anywhere.
		if n, _ := s.count(ctx); n == 0 {
			def = 1
		}
		_, err := s.st.DB.ExecContext(ctx, `INSERT INTO ai_providers
			(id, server_id, name, kind, model, base_url, api_key, command, mcp_config, is_default)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			p.ID, s.st.ServerID, p.Name, p.Kind, p.Model, p.BaseURL, p.SealedKey, p.Command, p.MCPConfig, def)
		if err != nil {
			return named(err)
		}
	} else {
		q := `UPDATE ai_providers SET name=?, kind=?, model=?, base_url=?, command=?, mcp_config=?, is_default=?,
			updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`
		args := []any{p.Name, p.Kind, p.Model, p.BaseURL, p.Command, p.MCPConfig, def}
		if p.SealedKey != "" {
			q += `, api_key=?`
			args = append(args, p.SealedKey)
		}
		q += ` WHERE id=? AND server_id=?`
		args = append(args, p.ID, s.st.ServerID)
		if _, err := s.st.DB.ExecContext(ctx, q, args...); err != nil {
			return named(err)
		}
	}
	if def == 1 {
		if err := s.SetDefault(ctx, p.ID); err != nil {
			return err
		}
	}
	return nil
}

// SetDefault makes one provider the default and the others not.
func (s *Service) SetDefault(ctx context.Context, id string) error {
	_, err := s.st.DB.ExecContext(ctx,
		`UPDATE ai_providers SET is_default = CASE WHEN id = ? THEN 1 ELSE 0 END WHERE server_id = ?`, id, s.st.ServerID)
	return err
}

// Delete removes one. If it was the default, the next one by name takes over:
// a server with providers left should always have a default, or every picker
// starts empty for no reason anybody asked for.
func (s *Service) Delete(ctx context.Context, id string) error {
	p, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if _, err := s.st.DB.ExecContext(ctx, `DELETE FROM ai_providers WHERE id = ? AND server_id = ?`, id, s.st.ServerID); err != nil {
		return err
	}
	if !p.Default {
		return nil
	}
	list, err := s.List(ctx)
	if err != nil || len(list) == 0 {
		return err
	}
	return s.SetDefault(ctx, list[0].ID)
}

func (s *Service) count(ctx context.Context) (int, error) {
	var n int
	err := s.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM ai_providers WHERE server_id = ?`, s.st.ServerID).Scan(&n)
	return n, err
}

// named turns the unique index into the sentence it means.
func named(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return errors.New("a provider with that name already exists")
	}
	return err
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
