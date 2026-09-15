package assistant

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/isletdev/islet/internal/store"
)

func testChats(t *testing.T) *Chats {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "islet.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewChats(st)
}

// The point of keeping a conversation here rather than in a tab: it is still
// there on the other device, and after the daemon restarts.
func TestAConversationOutlivesTheBrowserThatStartedIt(t *testing.T) {
	c := testChats(t)
	ctx := context.Background()
	ch, err := c.Create(ctx, "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Append(ctx, "alice", ch.ID,
		Message{Role: RoleUser, Text: "how many domains are on this server?"},
		Message{Role: RoleAssistant, Text: "Eleven."},
	); err != nil {
		t.Fatal(err)
	}

	got, msgs, err := c.Get(ctx, "alice", ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].Text != "how many domains are on this server?" || msgs[1].Text != "Eleven." {
		t.Fatalf("messages came back as %+v", msgs)
	}
	// A conversation nobody named is named by what was asked, or the list is a
	// column of "New chat".
	if got.Title != "how many domains are on this server?" {
		t.Errorf("title %q", got.Title)
	}
}

// Tool calls and their results are part of the record. They are what the
// assistant actually did to the server, which is the half worth keeping.
func TestToolCallsAndResultsSurviveTheRoundTrip(t *testing.T) {
	c := testChats(t)
	ctx := context.Background()
	ch, _ := c.Create(ctx, "alice", "")
	turn := Message{Role: RoleAssistant, Calls: []ToolCall{{ID: "t1", Name: "create_domain", Input: map[string]any{"host": "shop.example.com"}}}}
	results := Message{Role: RoleUser, Results: []ToolResult{{CallID: "t1", Content: "scopes do not cover domains", IsError: true}}}
	if err := c.Append(ctx, "alice", ch.ID, turn, results); err != nil {
		t.Fatal(err)
	}
	_, msgs, err := c.Get(ctx, "alice", ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || len(msgs[0].Calls) != 1 || msgs[0].Calls[0].Name != "create_domain" {
		t.Fatalf("the call did not survive: %+v", msgs)
	}
	if msgs[0].Calls[0].Input["host"] != "shop.example.com" {
		t.Errorf("arguments lost: %+v", msgs[0].Calls[0].Input)
	}
	if len(msgs[1].Results) != 1 || !msgs[1].Results[0].IsError {
		t.Errorf("the refusal did not survive: %+v", msgs[1].Results)
	}
}

// Several conversations at once is the ordinary case — one waiting on a deploy
// while another asks a question — so ordering is by activity, not by creation.
func TestListingIsByMostRecentActivity(t *testing.T) {
	c := testChats(t)
	ctx := context.Background()
	first, _ := c.Create(ctx, "alice", "")
	second, _ := c.Create(ctx, "alice", "")
	if err := c.Append(ctx, "alice", first.ID, Message{Role: RoleUser, Text: "deploy the shop"}); err != nil {
		t.Fatal(err)
	}
	list, err := c.List(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("want both conversations, got %d", len(list))
	}
	if list[0].ID != first.ID {
		t.Errorf("the one just used is not first: %v", list[0].ID)
	}
	if list[0].Messages != 1 || list[1].Messages != 0 {
		t.Errorf("message counts %d and %d", list[0].Messages, list[1].Messages)
	}
	_ = second
}

// A transcript carries whatever the tools returned — file contents, container
// environments, a database listing. Another account reading it would be reading
// those, so a conversation is only ever its owner's.
func TestAConversationIsOnlyItsOwners(t *testing.T) {
	c := testChats(t)
	ctx := context.Background()
	ch, _ := c.Create(ctx, "alice", "")
	_ = c.Append(ctx, "alice", ch.ID, Message{Role: RoleUser, Text: "cat /etc/shadow"})

	if _, _, err := c.Get(ctx, "bob", ch.ID); !errors.Is(err, ErrNoChat) {
		t.Errorf("bob could read alice's conversation: %v", err)
	}
	if err := c.Append(ctx, "bob", ch.ID, Message{Role: RoleUser, Text: "and again"}); !errors.Is(err, ErrNoChat) {
		t.Errorf("bob could write into alice's conversation: %v", err)
	}
	if err := c.Delete(ctx, "bob", ch.ID); !errors.Is(err, ErrNoChat) {
		t.Errorf("bob could delete alice's conversation: %v", err)
	}
	if err := c.Rename(ctx, "bob", ch.ID, "mine now"); !errors.Is(err, ErrNoChat) {
		t.Errorf("bob could rename alice's conversation: %v", err)
	}
	if list, _ := c.List(ctx, "bob"); len(list) != 0 {
		t.Errorf("bob sees %d of alice's conversations", len(list))
	}
}

// Deleting takes the messages with it rather than leaving them behind under a
// conversation that no longer exists.
func TestDeletingTakesTheMessagesWithIt(t *testing.T) {
	c := testChats(t)
	ctx := context.Background()
	ch, _ := c.Create(ctx, "alice", "")
	_ = c.Append(ctx, "alice", ch.ID, Message{Role: RoleUser, Text: "hello"})
	if err := c.Delete(ctx, "alice", ch.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := c.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM assistant_messages WHERE chat_id = ?`, ch.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d messages outlived their conversation", n)
	}
}

func TestTitlesAreReadableInAList(t *testing.T) {
	long := "Deploy the shop from github.com/me/shop, give it a Postgres, put it on shop.example.com and set up a nightly backup"
	got := Title(long)
	if len([]rune(got)) > 72 {
		t.Errorf("title is %d characters: %q", len([]rune(got)), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("a trimmed title should say it was trimmed: %q", got)
	}
	if strings.Contains(Title("two\nlines  and   spaces"), "\n") {
		t.Error("a title with a newline in it breaks the row it sits in")
	}
	if got := Title("two\nlines  and   spaces"); got != "two lines and spaces" {
		t.Errorf("whitespace not collapsed: %q", got)
	}
}
