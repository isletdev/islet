package cmdrun

import (
	"strings"
	"testing"
)

// Display is what the audit table and the command-transparency drawer store, so
// anything it leaves in is written down and shown back. These are the argv
// shapes the daemon actually builds, taken from the call sites rather than
// invented.
func TestDisplayRemovesSecrets(t *testing.T) {
	const pw = "hunter2-the-actual-password"
	const tok = "glrt-SECRETRUNNERTOKEN"

	cases := []struct {
		name string
		argv []string
		gone string // must not appear in the output
	}{{
		name: "mysql, attached long form (db.go)",
		argv: []string{"docker", "exec", "-i", "pg", "mysql", "--user=root", "--password=" + pw, "-N", "-B"},
		gone: pw,
	}, {
		name: "mysqldump, attached long form (db.go)",
		argv: []string{"docker", "exec", "-i", "my", "mysqldump", "--user=root", "--password=" + pw, "--single-transaction", "shop"},
		gone: pw,
	}, {
		name: "mongosh, separate long form (db.go)",
		argv: []string{"docker", "exec", "-i", "mg", "mongosh", "--quiet", "--username", "root", "--password", pw, "--authenticationDatabase", "admin"},
		gone: pw,
	}, {
		name: "mongodump, separate long form (db.go)",
		argv: []string{"mongodump", "--username", "root", "--password", pw, "--authenticationDatabase", "admin", "--archive"},
		gone: pw,
	}, {
		name: "redis-cli (db.go)",
		argv: []string{"docker", "exec", "-i", "rd", "redis-cli", "--pass", pw, "--no-auth-warning", "INFO"},
		gone: pw,
	}, {
		name: "gitlab runner registration (runner.go)",
		argv: []string{"docker", "exec", "r1", "gitlab-runner", "register", "--url", "https://gitlab.com", "--token", tok},
		gone: tok,
	}, {
		name: "an environment variable, which already worked",
		argv: []string{"docker", "exec", "-e", "REDIS_PASSWORD=" + pw, "rd", "redis-cli", "INFO"},
		gone: pw,
	}, {
		name: "credentials inside a URL, which already worked",
		argv: []string{"restic", "-r", "s3://key:" + pw + "@bucket/path"},
		gone: pw,
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Display(c.argv[0], c.argv[1:]...)
			if strings.Contains(got, c.gone) {
				t.Errorf("secret survived redaction:\n  %s", got)
			}
			if !strings.Contains(got, redacted) {
				t.Errorf("nothing was redacted at all:\n  %s", got)
			}
		})
	}
}

// The other half: a command the drawer is supposed to show in full. Redacting
// after every short flag would take these with it, which is why the rule is
// limited to long options whose name is a secret word.
func TestDisplayKeepsWhatIsNotASecret(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		keep string
	}{
		{"docker publishes a port with -p", []string{"docker", "run", "-p", "8080:80", "nginx"}, "8080:80"},
		{"compose names a project with -p", []string{"docker", "compose", "-p", "shop", "up", "-d"}, "shop"},
		{"timedatectl reads a property with -p", []string{"timedatectl", "show", "-p", "Timezone", "--value"}, "Timezone"},
		{"tmux prints a pane with -p", []string{"tmux", "capture-pane", "-p", "-t", "ws"}, "-t"},
		{"mongo names its auth database", []string{"mongosh", "--authenticationDatabase", "admin"}, "admin"},
		{"a flag that merely starts with pass", []string{"curl", "--passthrough", "value"}, "value"},
		{"an ordinary long flag", []string{"restic", "--repo", "/srv/backups"}, "/srv/backups"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Display(c.argv[0], c.argv[1:]...)
			if !strings.Contains(got, c.keep) {
				t.Errorf("redacted something it should have shown: want %q in\n  %s", c.keep, got)
			}
		})
	}
}

func TestRedactSingleArgument(t *testing.T) {
	cases := []struct{ in, want string }{
		{"RESTIC_PASSWORD=hunter2", "RESTIC_PASSWORD=" + redacted},
		{"--password=hunter2", "--password=" + redacted},
		{"MYSQL_ROOT_PASSWORD=hunter2", "MYSQL_ROOT_PASSWORD=" + redacted},
		{"AWS_ACCESS_KEY_ID=AKIA", "AWS_ACCESS_KEY_ID=" + redacted},
		{"s3://key:hunter2@bucket", "s3://key:" + redacted + "@bucket"},
		{"PATH=/usr/bin", "PATH=/usr/bin"},               // not a secret name
		{"--single-transaction", "--single-transaction"}, // no value at all
		{"", ""},
	}
	for _, c := range cases {
		if got := Redact(c.in); got != c.want {
			t.Errorf("Redact(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Creating a Mongo database sends the whole script as one argument, and the new
// user's password sits inside it. Neither the NAME=value rule nor the flag rule
// can see it there, so it reached the audit table and the command drawer in
// full — the exact failure the comment above Redact says this package prevents.
func TestASecretQuotedInsideAnArgumentIsRemoved(t *testing.T) {
	cases := []struct{ in, want string }{
		{
			`db.getSiblingDB("shop").createUser({user: "shop", pwd: "hunter2", roles: []})`,
			`db.getSiblingDB("shop").createUser({user: "shop", pwd: "<redacted>", roles: []})`,
		},
		{`{"token":"glpat-abc123"}`, `{"token":"<redacted>"}`},
		{`{password: 'hunter2'}`, `{password: '<redacted>'}`},
		// An argument that is itself NAME=value is already handled, and that
		// rule wins: the whole value goes, quotes and all.
		{`password = 'hunter2'`, `password =<redacted>`},
		// A word that merely starts with one of the names is not one of them.
		{`{"passthrough":"keep"}`, `{"passthrough":"keep"}`},
		{`{"tokenizer":"bpe"}`, `{"tokenizer":"bpe"}`},
		// Unquoted prose is left alone: redacting it would eat the sentence and
		// protect nothing, since there is no value there to protect.
		{`echo the password was reset`, `echo the password was reset`},
	}
	for _, c := range cases {
		if got := Redact(c.in); got != c.want {
			t.Errorf("Redact(%q)\n  = %q\n want %q", c.in, got, c.want)
		}
	}
}

// And the whole command line, as the drawer shows it.
func TestTheMongoCreateUserCommandCarriesNoPassword(t *testing.T) {
	js := `db.getSiblingDB("shop").createUser({user: "shop", pwd: "hunter2", roles: [{role: "readWrite", db: "shop"}]})`
	got := Display("docker", "exec", "-i", "mongo-1", "mongosh", "--quiet", "--username", "root", "--password", "rootsecret", "--authenticationDatabase", "admin", "--eval", js)
	for _, secret := range []string{"hunter2", "rootsecret"} {
		if strings.Contains(got, secret) {
			t.Errorf("%s survived into the command log:\n%s", secret, got)
		}
	}
	// It still has to read as the command it was.
	if !strings.Contains(got, "createUser") || !strings.Contains(got, "mongosh") {
		t.Errorf("the command is no longer legible:\n%s", got)
	}
}
