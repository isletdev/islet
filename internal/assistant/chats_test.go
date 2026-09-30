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

// A stopped answer and a finished one are the same rows with the same shape, so
// the difference has to be written down at the moment it is known. Everything
// the panel offers afterwards — Continue, or nothing at all — hangs off this
// one flag.
func TestAStoppedAnswerIsMarkedAndAFinishedOneIsNot(t *testing.T) {
	c := testChats(t)
	ctx := context.Background()
	ch, err := c.Create(ctx, "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Append(ctx, "alice", ch.ID,
		Message{Role: RoleUser, Text: "walk the whole deploy log"},
		Message{Role: RoleAssistant, Text: "The build starts at 10:02 and"},
	); err != nil {
		t.Fatal(err)
	}

	// Nothing is marked until the run says how it ended.
	_, msgs, err := c.Get(ctx, "alice", ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if msgs[1].Partial {
		t.Error("a turn was born unfinished")
	}

	if err := c.MarkLastPartial(ctx, "alice", ch.ID); err != nil {
		t.Fatal(err)
	}
	_, msgs, err = c.Get(ctx, "alice", ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !msgs[1].Partial {
		t.Error("the answer that was cut off is not marked, so the panel cannot offer to carry on")
	}
	if msgs[0].Partial {
		t.Error("the question was marked too")
	}
	if msgs[1].Text != "The build starts at 10:02 and" {
		t.Errorf("marking the turn rewrote it: %q", msgs[1].Text)
	}

	// The next answer in the same conversation is a fresh turn and carries
	// nothing from the one before it.
	if err := c.Append(ctx, "alice", ch.ID, Message{Role: RoleAssistant, Text: "…continues at 10:04."}); err != nil {
		t.Fatal(err)
	}
	_, msgs, err = c.Get(ctx, "alice", ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if msgs[2].Partial {
		t.Error("the turn after a stopped one inherited its mark")
	}
}

// A run that died before the model wrote anything leaves the question as the
// last turn. A question is not a half-finished answer: marking it would put a
// Continue button under something there is nothing to continue.
func TestAQuestionWithNoAnswerIsNotMarkedAsUnfinished(t *testing.T) {
	c := testChats(t)
	ctx := context.Background()
	ch, err := c.Create(ctx, "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Append(ctx, "alice", ch.ID, Message{Role: RoleUser, Text: "why is the shop down?"}); err != nil {
		t.Fatal(err)
	}
	if err := c.MarkLastPartial(ctx, "alice", ch.ID); err != nil {
		t.Fatal(err)
	}
	_, msgs, err := c.Get(ctx, "alice", ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if msgs[0].Partial {
		t.Error("a question was marked as an unfinished answer")
	}
}

// The mark is a write, and a write on somebody else's conversation is refused
// exactly as an append to it is.
func TestMarkingAnotherPersonsConversationIsRefused(t *testing.T) {
	c := testChats(t)
	ctx := context.Background()
	ch, err := c.Create(ctx, "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Append(ctx, "alice", ch.ID, Message{Role: RoleAssistant, Text: "half an answer"})
	if err := c.MarkLastPartial(ctx, "bob", ch.ID); !errors.Is(err, ErrNoChat) {
		t.Errorf("bob could mark alice's conversation: %v", err)
	}
}
