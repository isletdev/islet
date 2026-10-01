package github

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Putting a directory on GitHub.
//
// The other direction from everything else here: not "connect a repository that
// exists" but "this is an application somebody just wrote on this server, give
// it a repository". It is the half of the loop an agent needs — the assistant
// writes an app into a workspace, and this is the step that turns the result
// into something with a URL, a webhook and a deploy on every push.
//
// Every git command goes through cmdrun like everything else, which matters
// more here than usual: the push URL carries a credential, and cmdrun's
// redaction is what keeps it out of the audit table and the transparency
// drawer.

// Published is what happened.
type Published struct {
	Repo    Repo   `json:"repo"`
	HTMLURL string `json:"htmlUrl"`
	Branch  string `json:"branch"`
	// Ignored is true when Islet wrote a .gitignore because there was none.
	Ignored bool `json:"wroteGitignore"`
	// Commits is how many the repository had to be given: 0 when the directory
	// was already a repository with history.
	Commits int `json:"commits"`
}

// defaultIgnore is what nobody wants in their first commit, and what is
// genuinely dangerous there.
//
// `.env` is the line that matters. Everything else is tidiness; that one is an
// application's secrets going to GitHub because somebody said "publish this"
// about a directory they had been running locally.
const defaultIgnore = `# Written by Islet when this was first published.
node_modules/
vendor/
__pycache__/
*.pyc
dist/
build/
.next/
.nuxt/
.cache/
.DS_Store
*.log

# Secrets. Islet keeps an app's environment itself; it does not belong here.
.env
.env.*
!.env.example
`

// DefaultIgnore is what a published directory ignores when it had no
// .gitignore of its own. Exported so a test can assert the one line that
// matters is in it.
func DefaultIgnore() string { return defaultIgnore }

// Publish creates a repository and pushes a directory into it.
func (c *Client) Publish(ctx context.Context, actor, dir string, in NewRepo, branch string) (*Published, error) {
	if c.cmds == nil {
		return nil, errors.New("this daemon cannot run git")
	}
	dir = strings.TrimSpace(dir)
	if !filepath.IsAbs(dir) {
		return nil, errors.New("give an absolute path to the directory to publish")
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory on this server", dir)
	}
	if branch = strings.TrimSpace(branch); branch == "" {
		branch = "main"
	}
	// Before the directory is touched. Preparing it writes a .gitignore and
	// makes a commit, and discovering afterwards that there is no credential
	// leaves somebody's folder changed by a thing that did not happen — with a
	// git error about a temporary file in its place, which is what this
	// answered when it was tried with no connection at all.
	if c.Token(ctx) == "" {
		return nil, errors.New("creating a repository needs a GitHub token; add one under Settings → GitHub")
	}
	if strings.TrimSpace(in.Name) == "" {
		return nil, errors.New("a repository needs a name")
	}

	out := &Published{Branch: branch}
	if err := c.prepare(ctx, actor, dir, branch, out); err != nil {
		return nil, err
	}

	repo, err := c.CreateRepo(ctx, in)
	if err != nil {
		return nil, err
	}
	out.Repo = repo
	out.HTMLURL = "https://github.com/" + repo.FullName

	user, token, err := c.PushCredential(ctx, repo.FullName)
	if err != nil {
		return out, err
	}
	// The credential goes in the URL of this one command and nowhere else: not
	// into the remote, not into a config file, not onto the disk. `origin` is
	// set to the plain URL afterwards, which is what a person cloning or
	// pushing by hand will use.
	push := fmt.Sprintf("https://%s:%s@github.com/%s.git", user, token, repo.FullName)
	if err := c.pushTo(ctx, actor, dir, push, repo.URL, branch); err != nil {
		return out, fmt.Errorf("the repository was created but the push failed: %w", err)
	}
	return out, nil
}

// prepare makes the directory into something there is anything to push from:
// a repository, with a .gitignore, with its work committed.
//
// Separate from the GitHub half because it is the half that can be tested —
// against a bare repository in a temporary directory, with real git, no account
// and no network.
func (c *Client) prepare(ctx context.Context, actor, dir, branch string, out *Published) error {
	// Before anything is written. A .gitignore put here first is itself a file
	// to commit, so an empty directory would be published as a repository
	// containing nothing but the thing Islet added — and "publish this
	// directory" is a sentence somebody says about the wrong path.
	empty, err := emptyApartFromGit(dir)
	if err != nil {
		return err
	}
	if empty {
		return fmt.Errorf("%s has nothing in it to publish", dir)
	}
	if err := c.ensureIgnore(dir, out); err != nil {
		return err
	}
	// A repository, if this directory is one already — and *this* directory,
	// not one it happens to sit inside.
	//
	// `rev-parse --git-dir` answers for the nearest repository up the tree, so
	// publishing /srv/shop/api out of a monorepo, or anything at all on a
	// machine where /tmp is a checkout, found the parent, staged the parent's
	// entire worktree with `add -A`, committed into it, and pushed that to the
	// new repository. Found because this machine's /tmp is a git repository and
	// a test went looking for a file that was therefore never written.
	//
	// `--show-toplevel` is the question that was meant: where does the
	// repository containing this directory begin. If it does not begin here,
	// here is not a repository yet.
	if !c.isRepoRoot(ctx, actor, dir) {
		if _, err := c.git(ctx, actor, dir, "init", "-b", branch); err != nil {
			return fmt.Errorf("could not make %s a git repository: %w", dir, err)
		}
	}
	// Something to push. An existing repository with history is left exactly as
	// it is — publishing is not a reason to rewrite somebody's commits — and a
	// directory with uncommitted work gets one commit for it.
	_, headErr := c.git(ctx, actor, dir, "rev-parse", "--verify", "HEAD")
	if _, err := c.git(ctx, actor, dir, "add", "-A"); err != nil {
		return err
	}
	staged, _ := c.git(ctx, actor, dir, "diff", "--cached", "--name-only")
	if strings.TrimSpace(staged) != "" {
		if _, err := c.git(ctx, actor, dir,
			"-c", "user.name=Islet", "-c", "user.email=islet@localhost",
			"commit", "-m", commitMessage(headErr == nil)); err != nil {
			return fmt.Errorf("could not commit %s: %w", dir, err)
		}
		out.Commits++
	}
	if headErr != nil && out.Commits == 0 {
		return fmt.Errorf("%s has nothing in it to publish", dir)
	}
	return nil
}

// isRepoRoot is whether this directory is the top of a git repository, rather
// than somewhere inside one.
func (c *Client) isRepoRoot(ctx context.Context, actor, dir string) bool {
	top, err := c.git(ctx, actor, dir, "rev-parse", "--show-toplevel")
	if err != nil || top == "" {
		return false
	}
	// Through symlinks on both sides: /tmp is one on some systems, and a path
	// that differs only by that is the same directory.
	a, err1 := filepath.EvalSymlinks(top)
	b, err2 := filepath.EvalSymlinks(dir)
	if err1 != nil || err2 != nil {
		return filepath.Clean(top) == filepath.Clean(dir)
	}
	return a == b
}

// pushTo sends the branch, then points origin at the address a person would
// use rather than the one carrying a credential.
func (c *Client) pushTo(ctx context.Context, actor, dir, pushURL, originURL, branch string) error {
	if _, err := c.git(ctx, actor, dir, "push", pushURL, "HEAD:refs/heads/"+branch); err != nil {
		return err
	}
	_, _ = c.git(ctx, actor, dir, "remote", "remove", "origin")
	_, _ = c.git(ctx, actor, dir, "remote", "add", "origin", originURL)
	// Upstream written directly rather than through --set-upstream-to, which
	// needs the remote ref to exist locally and so needs a fetch — a fetch
	// against a private repository with no credential in reach is a command
	// that sits there waiting for a password nobody is going to type. Found by
	// a test that hung.
	_, _ = c.git(ctx, actor, dir, "config", "branch."+branch+".remote", "origin")
	_, _ = c.git(ctx, actor, dir, "config", "branch."+branch+".merge", "refs/heads/"+branch)
	return nil
}

func commitMessage(hadHistory bool) string {
	if hadHistory {
		return "Everything not yet committed, before publishing to GitHub"
	}
	return "Initial commit"
}

// emptyApartFromGit is whether there is anything here worth a repository.
func emptyApartFromGit(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.Name() != ".git" {
			return false, nil
		}
	}
	return true, nil
}

// ensureIgnore writes a .gitignore when there is none, and adds the two lines
// that matter to one that exists without them.
//
// Appending to somebody's file is a liberty, and it is taken for exactly one
// reason: the alternative is an application's .env arriving on GitHub because
// somebody asked for a directory to be published. Tidiness is only added to a
// file this wrote.
func (c *Client) ensureIgnore(dir string, out *Published) error {
	path := filepath.Join(dir, ".gitignore")
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if err := os.WriteFile(path, []byte(defaultIgnore), 0o644); err != nil {
			return err
		}
		out.Ignored = true
		return nil
	}
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, line := range strings.Split(string(body), "\n") {
		have[strings.TrimSpace(line)] = true
	}
	var add []string
	for _, line := range []string{".env", ".env.*"} {
		if !have[line] {
			add = append(add, line)
		}
	}
	if len(add) == 0 {
		return nil
	}
	text := string(body)
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += "\n# Added by Islet: an application's secrets do not belong in its repository.\n" + strings.Join(add, "\n") + "\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return err
	}
	out.Ignored = true
	return nil
}

// git runs one command in a directory and returns its output.
//
// Nothing here may ever ask for a password. A daemon has no terminal to ask on,
// and a git command that decides to try will wait for an answer until something
// kills it — holding whatever asked for it, which for a publish is a request
// somebody is watching. So credential helpers are switched off and askpass is a
// program that answers nothing: a command that needs a credential it was not
// given fails at once and says so.
func (c *Client) git(ctx context.Context, actor, dir string, args ...string) (string, error) {
	full := append([]string{
		"-C", dir,
		"-c", "credential.helper=",
		"-c", "core.askpass=/bin/echo",
	}, args...)
	res, err := c.cmds.Run(ctx, actor, "git", full...)
	if err != nil {
		msg := strings.TrimSpace(res.Stderr)
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(firstLine(msg))
	}
	return strings.TrimSpace(res.Stdout), nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
