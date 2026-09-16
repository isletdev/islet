package db

import (
	"strings"
	"testing"
)

// Dropping a database is the most destructive thing this package does, and the
// name it is given comes from an HTTP request. Two gates stand in front of it:
// the name has to look like an identifier, and it must not be one of the
// databases the instance cannot lose.
func TestDropRefusesNamesItShouldNeverAccept(t *testing.T) {
	// The identifier gate. A name that gets past this is interpolated into SQL.
	for _, bad := range []string{
		"", " ", "shop; DROP DATABASE postgres", `shop" ; --`, "shop'", "shop`",
		"9shop", "shop-1", "shop.public", "../etc", "shop\nDROP",
		strings.Repeat("a", 64),
	} {
		if identRe.MatchString(bad) {
			t.Errorf("%q passes as a database name, and that name reaches SQL", bad)
		}
	}
	for _, ok := range []string{"shop", "_internal", "a", "shop_2026", strings.Repeat("a", 63)} {
		if !identRe.MatchString(ok) {
			t.Errorf("%q is a perfectly ordinary database name and was refused", ok)
		}
	}
}

// The names a drop must refuse even when they are valid identifiers: the
// instance's own database, and the engines' system databases. Losing one of
// these does not lose a database, it loses the server.
func TestTheSystemDatabasesAreNamedInOnePlace(t *testing.T) {
	inst := &Instance{Name: "pg", Engine: "postgres", Database: "app"}
	for _, name := range []string{"app", "postgres", "mysql", "admin", "local", "config"} {
		err := (&Service{}).DropDatabase(t.Context(), "tester", inst, name)
		if err == nil {
			t.Errorf("dropping %q was allowed", name)
			continue
		}
		if !strings.Contains(err.Error(), "system database") && !strings.Contains(err.Error(), "primary") {
			t.Errorf("dropping %q failed for the wrong reason: %v", name, err)
		}
	}
}

// A dump file name comes from a URL and is joined to a directory on disk. It
// must not be able to name a file outside it.
func TestADumpNameCannotClimbOutOfItsDirectory(t *testing.T) {
	s := &Service{dumpDir: t.TempDir()}
	inst := &Instance{Name: "pg"}
	for _, bad := range []string{
		"", "..", "../../etc/shadow", "a/b", `a\b`, ".hidden", "./x",
	} {
		if _, err := s.DumpPath(inst, bad); err == nil {
			t.Errorf("%q was accepted as a dump file name", bad)
		}
	}
	// A name that is merely absent fails as not found rather than as invalid,
	// so the two cases stay distinguishable to the caller.
	if _, err := s.DumpPath(inst, "2026-09-16.sql.gz"); err != ErrNotFound {
		t.Errorf("a well-formed name that is not there gave %v, want ErrNotFound", err)
	}
}

// What each engine is actually asked to do, and — the part worth a test — what
// never appears on a command line that gets written to the audit log.
func TestDumpArgvPerEngine(t *testing.T) {
	inst := &Instance{Name: "x", RootUser: "root", RootPass: "hunter2"}
	for _, c := range []struct {
		engine, wantHead, wantExt string
	}{
		{"postgres", "pg_dump", ".sql.gz"},
		{"mysql", "mysqldump", ".sql.gz"},
		{"mongo", "mongodump", ".archive.gz"},
		{"redis", "sh", ".rdb.gz"},
	} {
		inst.Engine = c.engine
		argv, ext, err := dumpArgv(inst, "shop")
		if err != nil {
			t.Errorf("%s: %v", c.engine, err)
			continue
		}
		if argv[0] != c.wantHead {
			t.Errorf("%s dumps with %q", c.engine, argv[0])
		}
		if ext != c.wantExt {
			t.Errorf("%s writes %q", c.engine, ext)
		}
	}
	inst.Engine = "sqlite"
	if _, _, err := dumpArgv(inst, "shop"); err == nil {
		t.Error("an engine with no dump support returned a command anyway")
	}
}

// Redis takes its password from the environment rather than the command line.
// Everything on a command line is written to the audit table and shown in the
// transparency drawer, and cmdrun's redaction covers the shapes the other
// engines use — but not a password inside a shell one-liner.
func TestRedisDoesNotPutItsPasswordOnTheCommandLine(t *testing.T) {
	inst := &Instance{Name: "cache", Engine: "redis", RootPass: "hunter2"}
	argv, _, err := dumpArgv(inst, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(argv, " "), "hunter2") {
		t.Errorf("the password is on the command line: %v", argv)
	}
	if !strings.Contains(strings.Join(argv, " "), "$REDIS_PASSWORD") {
		t.Errorf("the password should come from the environment: %v", argv)
	}
}

// A connection URL is shown to people and pasted into applications; the
// password in it has to survive being a URL.
func TestConnectionURLsEscapeWhatIsInThem(t *testing.T) {
	s := &Service{}
	inst := &Instance{Engine: "postgres"}
	got := s.connURL(inst, "db-host", 5432, "shop", "p@ss:w/rd?x", "shop")
	if strings.Contains(got, "p@ss:w/rd?x") {
		t.Errorf("the password went in raw and the URL is now ambiguous: %s", got)
	}
	if !strings.HasPrefix(got, "postgres://") || !strings.Contains(got, "db-host:5432") {
		t.Errorf("unexpected URL: %s", got)
	}
}
