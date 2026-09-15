package deploy

import (
	"context"
	"strings"
	"testing"
)

// A deploy fills @vault:NAME in on the way into the process. The hook is what
// keeps this package from knowing anything about the vault, and it has to be
// applied after the groups so a value that arrived from a group can refer to a
// secret too.
func TestSecretsAreFilledInFromTheHook(t *testing.T) {
	s := &Service{
		EnvGroup: func(_ context.Context, name string) ([]string, bool) {
			if name == "shared" {
				return []string{"FROM_GROUP=@vault:GROUP_SECRET"}, true
			}
			return nil, false
		},
		Secrets: func(_ context.Context, in string) string {
			in = strings.ReplaceAll(in, "@vault:DB_PASS", "hunter2")
			return strings.ReplaceAll(in, "@vault:GROUP_SECRET", "from-a-group")
		},
	}
	lg := &logger{rn: &run{}}
	out := s.expandGroups(context.Background(), []string{"DATABASE_URL=postgres://u:@vault:DB_PASS@db/shop", "PLAIN=unchanged", "@shared"}, lg)

	got := strings.Join(out, "\n")
	if !strings.Contains(got, "postgres://u:hunter2@db/shop") {
		t.Errorf("the reference was not filled in: %q", got)
	}
	if !strings.Contains(got, "FROM_GROUP=from-a-group") {
		t.Errorf("a group's value should be expanded too: %q", got)
	}
	if !strings.Contains(got, "PLAIN=unchanged") {
		t.Errorf("a line with no reference must be left alone: %q", got)
	}
	// The log says that a substitution happened and never what it substituted.
	if strings.Contains(lg.b.String(), "hunter2") {
		t.Fatal("a secret reached the deploy log")
	}
	if !strings.Contains(lg.b.String(), "from the vault") {
		t.Errorf("the log should say a substitution happened: %q", lg.b.String())
	}
}

// Without a vault wired in, a reference stays exactly as written. An empty
// password is a working connection to the wrong place; @vault:X fails at once.
func TestWithoutTheHookAReferenceIsLeftAlone(t *testing.T) {
	s := &Service{}
	out := s.expandGroups(context.Background(), []string{"P=@vault:NOPE"}, &logger{rn: &run{}})
	if len(out) != 1 || out[0] != "P=@vault:NOPE" {
		t.Errorf("got %q", out)
	}
}
