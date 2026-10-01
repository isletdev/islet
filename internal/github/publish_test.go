package github

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/isletdev/islet/internal/cmdrun"
	"github.com/isletdev/islet/internal/store"
)

// The half of publishing that does not need GitHub: making a directory into a
// repository with a commit in it, and pushing that somewhere. Tested against a
// bare repository in a temporary directory with real git, because the thing
// worth knowing is whether these commands work, not whether a mock agrees with
// the code that calls it.
func publisher(t *testing.T) *Client {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on this machine")
	}
	// A real store, because every command goes through cmdrun and cmdrun
	// writes each one down — which is the arrangement being relied on here as
	// much as anywhere else.
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "islet.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &Client{st: st, cmds: cmdrun.New(st, slog.New(slog.NewTextHandler(io.Discard, nil)))}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestADirectoryBecomesARepositoryWithOneCommit(t *testing.T) {
	c := publisher(t)
	dir := t.TempDir()
	write(t, dir, "index.js", "console.log('hello')\n")
	write(t, dir, "src/app.js", "export const x = 1\n")

	var out Published
	if err := c.prepare(context.Background(), "tester", dir, "main", &out); err != nil {
		t.Fatal(err)
	}
	if out.Commits != 1 {
		t.Errorf("%d commits, want 1", out.Commits)
	}
	if !out.Ignored {
		t.Error("no .gitignore was written for a directory that had none")
	}
	branch, _ := c.git(context.Background(), "t", dir, "rev-parse", "--abbrev-ref", "HEAD")
	if branch != "main" {
		t.Errorf("the branch is %q, not main", branch)
	}
	files, _ := c.git(context.Background(), "t", dir, "ls-tree", "-r", "--name-only", "HEAD")
	for _, want := range []string{"index.js", "src/app.js", ".gitignore"} {
		if !strings.Contains(files, want) {
			t.Errorf("%s is not in the commit:\n%s", want, files)
		}
	}
}

// The one that matters. "Publish this directory" is said about directories
// people have been running locally, and those have a .env in them.
func TestAnEnvFileIsNeverCommitted(t *testing.T) {
	c := publisher(t)
	ctx := context.Background()

	t.Run("no gitignore of its own", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "app.py", "print('hi')\n")
		write(t, dir, ".env", "DATABASE_URL=postgres://user:hunter2@localhost/shop\n")
		write(t, dir, ".env.production", "STRIPE_KEY=sk_live_whatever\n")
		var out Published
		if err := c.prepare(ctx, "tester", dir, "main", &out); err != nil {
			t.Fatal(err)
		}
		files, _ := c.git(ctx, "t", dir, "ls-tree", "-r", "--name-only", "HEAD")
		if strings.Contains(files, ".env") {
			t.Errorf("an env file was committed:\n%s", files)
		}
		if !strings.Contains(files, "app.py") {
			t.Error("the application itself was not committed")
		}
	})

	t.Run("a gitignore that does not mention it", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "app.py", "print('hi')\n")
		write(t, dir, ".env", "SECRET=hunter2\n")
		write(t, dir, ".gitignore", "node_modules/\n*.log\n")
		var out Published
		if err := c.prepare(ctx, "tester", dir, "main", &out); err != nil {
			t.Fatal(err)
		}
		files, _ := c.git(ctx, "t", dir, "ls-tree", "-r", "--name-only", "HEAD")
		if strings.Contains(files, ".env") {
			t.Errorf("an env file was committed past an existing .gitignore:\n%s", files)
		}
		// Somebody else's file is added to, not replaced.
		body, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "node_modules/") || !strings.Contains(string(body), "*.log") {
			t.Errorf("the existing .gitignore was overwritten:\n%s", body)
		}
	})

	t.Run("a gitignore that already has it", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "app.py", "x\n")
		write(t, dir, ".gitignore", ".env\n.env.*\n")
		var out Published
		if err := c.prepare(ctx, "tester", dir, "main", &out); err != nil {
			t.Fatal(err)
		}
		if out.Ignored {
			t.Error("a .gitignore that already said so was written to anyway")
		}
	})
}

// An existing repository is published as it is: its history is not rewritten,
// and only work nobody had committed gets a commit of its own.
func TestAnExistingRepositoryKeepsItsHistory(t *testing.T) {
	c := publisher(t)
	ctx := context.Background()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"-c", "user.name=T", "-c", "user.email=t@t", "commit", "--allow-empty", "-m", "first"},
		{"-c", "user.name=T", "-c", "user.email=t@t", "commit", "--allow-empty", "-m", "second"},
	} {
		if _, err := c.git(ctx, "t", dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := c.git(ctx, "t", dir, "rev-list", "--count", "HEAD")

	write(t, dir, "new.txt", "added since\n")
	var out Published
	if err := c.prepare(ctx, "tester", dir, "main", &out); err != nil {
		t.Fatal(err)
	}
	after, _ := c.git(ctx, "t", dir, "rev-list", "--count", "HEAD")
	if before != "2" || after != "3" {
		t.Errorf("history went from %s to %s commits, want 2 to 3", before, after)
	}
	if out.Commits != 1 {
		t.Errorf("%d commits made, want 1", out.Commits)
	}

	// And one with nothing uncommitted is published without a commit at all.
	var again Published
	if err := c.prepare(ctx, "tester", dir, "main", &again); err != nil {
		t.Fatal(err)
	}
	if again.Commits != 0 {
		t.Errorf("a clean repository was given %d commits", again.Commits)
	}
}

func TestAnEmptyDirectoryIsRefusedRatherThanPublished(t *testing.T) {
	c := publisher(t)
	var out Published
	err := c.prepare(context.Background(), "tester", t.TempDir(), "main", &out)
	if err == nil {
		t.Fatal("an empty directory was published")
	}
	if !strings.Contains(err.Error(), "nothing in it") {
		t.Errorf("the refusal reads %q", err)
	}
}

// The push itself, against a bare repository rather than GitHub: the branch
// arrives, and origin is left pointing at the plain address rather than the one
// the credential was in.
func TestThePushLandsAndOriginIsLeftClean(t *testing.T) {
	c := publisher(t)
	ctx := context.Background()
	remote := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "init", "--bare", "-b", "main", remote).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	dir := t.TempDir()
	write(t, dir, "index.html", "<h1>hi</h1>\n")
	var out Published
	if err := c.prepare(ctx, "tester", dir, "main", &out); err != nil {
		t.Fatal(err)
	}
	if err := c.pushTo(ctx, "tester", dir, remote, "https://github.com/someone/thing.git", "main"); err != nil {
		t.Fatal(err)
	}
	landed, err := exec.Command("git", "--git-dir", remote, "ls-tree", "-r", "--name-only", "main").CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, landed)
	}
	if !strings.Contains(string(landed), "index.html") {
		t.Errorf("the push did not land:\n%s", landed)
	}
	origin, _ := c.git(ctx, "t", dir, "remote", "get-url", "origin")
	if origin != "https://github.com/someone/thing.git" {
		t.Errorf("origin is %q", origin)
	}
	// Nothing anywhere in the repository's own configuration carries a
	// credential, because the push URL was an argument to one command.
	cfg, err := os.ReadFile(filepath.Join(dir, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cfg), "@github.com") && strings.Contains(string(cfg), ":") {
		if strings.Contains(string(cfg), "x-access-token") || strings.Contains(string(cfg), "ghp_") {
			t.Errorf("a credential was written into .git/config:\n%s", cfg)
		}
	}
}

// Nothing is touched before the things that can refuse have refused.
//
// Publishing writes a .gitignore and makes a commit. With no credential that is
// somebody's directory changed by an operation that then failed — and it
// failed, when this was first tried, with a git error about a temporary file,
// which says nothing about the missing token at all.
func TestPublishingRefusesBeforeItTouchesTheDirectory(t *testing.T) {
	c := publisher(t)
	dir := t.TempDir()
	write(t, dir, "app.py", "print('hi')\n")

	_, err := c.Publish(context.Background(), "tester", dir, NewRepo{Name: "thing"}, "main")
	if err == nil {
		t.Fatal("published with no GitHub connection at all")
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("the refusal does not mention the credential: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); err == nil {
		t.Error("a .gitignore was written into the directory by a publish that refused")
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		t.Error("the directory was made into a repository by a publish that refused")
	}
	// And a nameless repository is refused the same way, for the same reason.
	if _, err := c.Publish(context.Background(), "tester", dir, NewRepo{}, "main"); err == nil {
		t.Error("published without a repository name")
	}
}

// A directory inside somebody else's repository is published as itself.
//
// `git rev-parse --git-dir` answers for the nearest repository up the tree, so
// this used to find the parent, stage the parent's whole worktree, commit into
// it and push that — the entire monorepo, or everything in /tmp on a machine
// where that is a checkout, which is how this was found.
func TestADirectoryInsideAnotherRepositoryIsPublishedAlone(t *testing.T) {
	c := publisher(t)
	ctx := context.Background()
	parent := t.TempDir()
	if _, err := c.git(ctx, "t", parent, "init", "-b", "main"); err != nil {
		t.Fatal(err)
	}
	write(t, parent, "SECRET-FROM-THE-PARENT.txt", "not mine to publish\n")
	if _, err := c.git(ctx, "t", parent, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.git(ctx, "t", parent, "-c", "user.name=T", "-c", "user.email=t@t", "commit", "-m", "parent"); err != nil {
		t.Fatal(err)
	}

	child := filepath.Join(parent, "services", "api")
	write(t, child, "main.go", "package main\n")

	var out Published
	if err := c.prepare(ctx, "tester", child, "main", &out); err != nil {
		t.Fatal(err)
	}
	// Its own repository, rooted here.
	top, err := c.git(ctx, "t", child, "rev-parse", "--show-toplevel")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(child)
	got, _ := filepath.EvalSymlinks(top)
	if got != want {
		t.Fatalf("the repository root is %s, not the directory being published (%s)", got, want)
	}
	files, _ := c.git(ctx, "t", child, "ls-tree", "-r", "--name-only", "HEAD")
	if strings.Contains(files, "SECRET-FROM-THE-PARENT") {
		t.Errorf("the parent repository's files were committed:\n%s", files)
	}
	if !strings.Contains(files, "main.go") {
		t.Errorf("the directory's own files were not committed:\n%s", files)
	}
	// And the parent is untouched: one commit, nothing staged.
	n, _ := c.git(ctx, "t", parent, "rev-list", "--count", "HEAD")
	if n != "1" {
		t.Errorf("the parent repository now has %s commits", n)
	}
}
