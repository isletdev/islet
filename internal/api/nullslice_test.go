package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/isletdev/islet/internal/backup"
	"github.com/isletdev/islet/internal/catalog"
	"github.com/isletdev/islet/internal/db"
	"github.com/isletdev/islet/internal/deploy"
	"github.com/isletdev/islet/internal/files"
	"github.com/isletdev/islet/internal/notify"
	"github.com/isletdev/islet/internal/proxy"
	"github.com/isletdev/islet/internal/runner"
	"github.com/isletdev/islet/internal/security"
	"github.com/isletdev/islet/internal/uptime"
	"github.com/isletdev/islet/internal/workspace"
)

// A list the panel iterates must never arrive as null.
//
// There is no codegen here: the TypeScript types are written by hand against
// the Go ones, and they declare these fields required — so `null` is not a
// value the panel is prepared for, and `d.databases.map(...)` throws rather
// than rendering an empty list. A nil slice in Go marshals to null, and Go
// produces nil slices constantly: an early return, a query that found nothing,
// an error path that assigned before it checked.
//
// Four live crashes came from exactly this, and each one was on an error path —
// so the page broke at the moment it was supposed to explain what went wrong.
// The rule is the cheap one: a slice a client iterates is either absent
// (omitempty, which the TS type must then mark optional) or an empty array.
// Never null.
func TestNoResponseTypeMarshalsAListAsNull(t *testing.T) {
	for _, v := range []any{
		proxy.Domain{}, proxy.Location{},
		db.Instance{}, db.Database{},
		deploy.App{}, deploy.Release{},
		uptime.Check{},
		backup.Plan{}, backup.Destination{}, backup.Run{},
		runner.Pool{}, runner.Runner{},
		catalog.App{},
		security.Report{}, security.Check{},
		notify.Channel{}, notify.Event{},
		workspace.Workspace{}, workspace.Agent{},
		files.Entry{},
	} {
		typ := reflect.TypeOf(v)
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if f.Type.Kind() != reflect.Slice || f.Type.Elem().Kind() == reflect.Uint8 {
				continue // []byte is base64, not a list
			}
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "-" || name == "" {
				continue
			}
			if string(m[name]) == "null" {
				t.Errorf("%s.%s marshals to null; give it omitempty or make sure it is never nil", typ.Name(), f.Name)
			}
		}
	}
}
