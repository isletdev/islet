package deploy

import "testing"

func TestRefMatches(t *testing.T) {
	cases := []struct {
		rule, ref string
		want      bool
	}{
		{"main", "refs/heads/main", true},
		{"main", "refs/heads/dev", false},
		{"main", "refs/tags/v1.0.0", false},
		{"tag:v*", "refs/tags/v1.2.3", true},
		{"tag:v*", "refs/tags/release-1", false},
		{"tag:v*", "refs/heads/main", false},
		{"tag:*", "refs/tags/anything", true},
	}
	for _, c := range cases {
		if got := RefMatches(c.rule, c.ref); got != c.want {
			t.Errorf("RefMatches(%q, %q) = %v, want %v", c.rule, c.ref, got, c.want)
		}
	}
	if RefName("refs/tags/v1") != "v1" || RefName("refs/heads/main") != "main" {
		t.Error("RefName")
	}
	var a App
	a.Name, a.Source, a.RepoURL, a.Branch = "x", "git", "https://github.com/o/r", "tag:"
	if err := a.Validate(); err == nil {
		t.Error("empty tag pattern accepted")
	}
	a.Branch, a.Env = "tag:v*", "@shared\nKEY=v\n@Bad Name"
	if err := a.Validate(); err == nil {
		t.Error("bad group line accepted")
	}
	a.Env = "@shared\nKEY=v"
	if err := a.Validate(); err != nil {
		t.Errorf("valid app rejected: %v", err)
	}
}
