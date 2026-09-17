package assistant

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/store"
)

// Chat is one conversation, as the panel lists it.
type Chat struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// ProviderID is the model this conversation is having. Empty means the
	// server's default, which is what every conversation started before there
	// was a choice has — so an old chat carries on with what it was using.
	ProviderID string `json:"providerId,omitempty"`
	Messages   int    `json:"messages"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

// ErrNoChat is a conversation that is not there, or is not yours.
var ErrNoChat = errors.New("no such conversation")

// Chats keeps conversations on the server rather than in a browser tab.
//
// The transcript used to live in React state: lost on reload, and never on the
// other device. For this feature that is the wrong place twice over — a record
// of what the assistant did to a real server belongs beside the audit log, and
// the person asking is as likely to be holding a phone as sitting at the
// laptop that started the conversation.
type Chats struct {
	st  *store.Store
	now func() time.Time
}

func NewChats(st *store.Store) *Chats {
	return &Chats{st: st, now: time.Now}
}

// Create starts a conversation. The title is filled in from the first question
// when one is asked, so a new conversation does not demand to be named before
// it can be used.
func (c *Chats) Create(ctx context.Context, username, title string) (*Chat, error) {
	return c.CreateWith(ctx, username, title, "")
}

// CreateWith starts a conversation against a particular model.
//
// The id is stored rather than resolved now, because "which model" is a
// property of the conversation: changing the server's default later must not
// quietly move a conversation that is already under way onto another model.
func (c *Chats) CreateWith(ctx context.Context, username, title, providerID string) (*Chat, error) {
	id, err := chatID()
	if err != nil {
		return nil, err
	}
	now := c.now().UTC().Format(time.RFC3339Nano)
	_, err = c.st.DB.ExecContext(ctx,
		`INSERT INTO assistant_chats (id, server_id, username, title, provider_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, c.st.ServerID, username, Title(title), providerID, now, now)
	if err != nil {
		return nil, err
	}
	return &Chat{ID: id, Title: Title(title), ProviderID: providerID, CreatedAt: now, UpdatedAt: now}, nil
}

// List is one person's conversations, newest activity first.
//
// Conversations are per person, not per server: a transcript carries whatever
// the tools returned — file contents, container environments, a database
// listing — and another admin reading it would be reading those.
func (c *Chats) List(ctx context.Context, username string) ([]Chat, error) {
	rows, err := c.st.DB.QueryContext(ctx,
		`SELECT c.id, c.title, c.provider_id, c.created_at, c.updated_at, (SELECT COUNT(*) FROM assistant_messages m WHERE m.chat_id = c.id)
		 FROM assistant_chats c WHERE c.server_id = ? AND c.username = ? ORDER BY c.updated_at DESC`,
		c.st.ServerID, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Chat{}
	for rows.Next() {
		var ch Chat
		if err := rows.Scan(&ch.ID, &ch.Title, &ch.ProviderID, &ch.CreatedAt, &ch.UpdatedAt, &ch.Messages); err != nil {
			return nil, err
		}
		out = append(out, ch)
	}
	return out, rows.Err()
}

// Get returns a conversation and everything said in it, in order.
func (c *Chats) Get(ctx context.Context, username, id string) (*Chat, []Message, error) {
	var ch Chat
	err := c.st.DB.QueryRowContext(ctx,
		`SELECT id, title, provider_id, created_at, updated_at FROM assistant_chats WHERE id = ? AND server_id = ? AND username = ?`,
		id, c.st.ServerID, username).Scan(&ch.ID, &ch.Title, &ch.ProviderID, &ch.CreatedAt, &ch.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNoChat
	}
	if err != nil {
		return nil, nil, err
	}
	msgs, err := c.messages(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	ch.Messages = len(msgs)
	return &ch, msgs, nil
}

func (c *Chats) messages(ctx context.Context, id string) ([]Message, error) {
	rows, err := c.st.DB.QueryContext(ctx,
		`SELECT body FROM assistant_messages WHERE chat_id = ? AND server_id = ? ORDER BY seq`, id, c.st.ServerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var m Message
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			// One unreadable row must not make the conversation unreadable.
			continue
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Append adds turns to a conversation and moves it to the top of the list.
//
// It is called as each turn completes rather than once at the end, so a run
// interrupted by an update or a restart leaves behind what it had done instead
// of nothing at all.
func (c *Chats) Append(ctx context.Context, username, id string, msgs ...Message) error {
	if len(msgs) == 0 {
		return nil
	}
	tx, err := c.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var owner string
	err = tx.QueryRowContext(ctx, `SELECT username FROM assistant_chats WHERE id = ? AND server_id = ?`, id, c.st.ServerID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && owner != username) {
		return ErrNoChat
	}
	if err != nil {
		return err
	}
	var next int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq), -1) + 1 FROM assistant_messages WHERE chat_id = ? AND server_id = ?`, id, c.st.ServerID).Scan(&next); err != nil {
		return err
	}
	for _, m := range msgs {
		b, err := json.Marshal(m)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO assistant_messages (chat_id, server_id, seq, body, created_at) VALUES (?, ?, ?, ?, ?)`,
			id, c.st.ServerID, next, string(b), c.now().UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
		next++
	}
	// A conversation with no title takes one from its first question, so the
	// list reads as what was asked rather than as a column of "New chat".
	if _, err := tx.ExecContext(ctx,
		`UPDATE assistant_chats SET updated_at = ?,
		   title = CASE WHEN title = '' THEN ? ELSE title END
		 WHERE id = ? AND server_id = ?`,
		c.now().UTC().Format(time.RFC3339Nano), Title(firstText(msgs)), id, c.st.ServerID); err != nil {
		return err
	}
	return tx.Commit()
}

// Rename sets a title by hand.
func (c *Chats) Rename(ctx context.Context, username, id, title string) error {
	res, err := c.st.DB.ExecContext(ctx,
		`UPDATE assistant_chats SET title = ?, updated_at = ? WHERE id = ? AND server_id = ? AND username = ?`,
		Title(title), c.now().UTC().Format(time.RFC3339Nano), id, c.st.ServerID, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoChat
	}
	return nil
}

// Delete removes a conversation and everything in it.
func (c *Chats) Delete(ctx context.Context, username, id string) error {
	res, err := c.st.DB.ExecContext(ctx,
		`DELETE FROM assistant_chats WHERE id = ? AND server_id = ? AND username = ?`, id, c.st.ServerID, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoChat
	}
	// The messages go with it through the foreign key, which the daemon turns
	// on at every connection.
	return nil
}

// Title trims a question down to something a list can show.
func Title(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", " "))
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	const max = 70
	if len(s) <= max {
		return s
	}
	// Cut at a word boundary when there is one nearby, so a title does not end
	// mid-word for the sake of four characters.
	cut := s[:max]
	if i := strings.LastIndex(cut, " "); i > max-20 {
		cut = cut[:i]
	}
	return cut + "…"
}

func firstText(msgs []Message) string {
	for _, m := range msgs {
		if m.Role == RoleUser && strings.TrimSpace(m.Text) != "" {
			return m.Text
		}
	}
	return ""
}

func chatID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("no randomness for a conversation id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
