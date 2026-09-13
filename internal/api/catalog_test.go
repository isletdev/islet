package api

import "testing"

// A digest the panel cannot recognise must not read as "there is an update":
// buildx prints its whole human report when the format template is one it does
// not like, and comparing that report against a digest is never equal, so every
// installed app claimed an update was waiting for it, forever.
func TestDigestOf(t *testing.T) {
	report := "Name:      docker.io/traefik/whoami:latest\n" +
		"MediaType: application/vnd.oci.image.index.v1+json\n" +
		"Digest:    sha256:c4717a8d1f0134a7444e24f881160e033991f23027c6c5a9a3f8fd22e70d1d44\n"
	for _, tc := range []struct{ in, want string }{
		{"traefik/whoami@sha256:abc\n", "sha256:abc"},
		{"\"sha256:abc\"", "sha256:abc"},
		{"  sha256:abc  ", "sha256:abc"},
		{report, ""},
		{"", ""},
		{"<none>", ""},
		{"not-a-digest", ""},
	} {
		if got := digestOf(tc.in); got != tc.want {
			t.Errorf("digestOf(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
