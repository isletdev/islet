package docker

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// Docker prints Links as a string — "1", not 1 — and the struct that read this
// declared it an int. encoding/json fails the whole document on one bad field,
// so sizes came back empty and every volume on the machine reported itself
// unused. That is the one thing this view exists to say, and it was saying the
// answer that makes deleting a volume look safe.
func TestVolumeLinksAreReadFromDockersOwnShape(t *testing.T) {
	// Copied from `docker system df -v --format {{json .}}` on a live host.
	const out = `{"Volumes":[{"Name":"poolse_pg_data","Size":"68.36MB","Links":"1","Driver":"local"},` +
		`{"Name":"old_scratch","Size":"12MB","Links":"0","Driver":"local"}]}`

	var df struct {
		Volumes []struct {
			Name, Size, Links string
		}
	}
	if err := json.Unmarshal([]byte(out), &df); err != nil {
		t.Fatalf("Docker's own output did not parse: %v", err)
	}
	if len(df.Volumes) != 2 {
		t.Fatalf("got %d volumes", len(df.Volumes))
	}
	want := map[string]bool{"poolse_pg_data": true, "old_scratch": false}
	for _, v := range df.Volumes {
		n, err := strconv.Atoi(strings.TrimSpace(v.Links))
		inUse := err == nil && n > 0
		if inUse != want[v.Name] {
			t.Errorf("%s: inUse=%v, want %v", v.Name, inUse, want[v.Name])
		}
		if v.Size == "" {
			t.Errorf("%s: no size", v.Name)
		}
	}
}
