// Package db makes catalog-installed database stacks first-class objects:
// connection strings, databases and users, dumps and restores, extension
// toggles and public exposure. Everything runs through docker exec inside
// the instance's own container, so no client tools are needed on the host.
package db

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/isletdev/islet/internal/catalog"
	"github.com/isletdev/islet/internal/cmdrun"
	"github.com/isletdev/islet/internal/docker"
	"github.com/isletdev/islet/internal/proxy"
)

// Engines Islet understands, keyed by catalog slug.
var engines = map[string]string{"postgres": "postgres", "mysql": "mysql", "mariadb": "mysql", "redis": "redis", "valkey": "redis", "mongodb": "mongo"}

// Instance is one database server created from the catalog.
type Instance struct {
	Name      string `json:"name"` // stack name
	Slug      string `json:"slug"`
	Engine    string `json:"engine"` // postgres | mysql | redis | mongo
	Container string `json:"container"`
	State     string `json:"state"`
	Image     string `json:"image"`
	Port      int    `json:"port"`
	Network   string `json:"network"`
	Public    string `json:"public,omitempty"`    // host:port when published
	Pooler    bool   `json:"pooler"`              // PgBouncer service present
	AllowFrom string `json:"allowFrom,omitempty"` // firewall allowlist for the published port
	Pooled    string `json:"pooledUrl,omitempty"`
	User      string `json:"user"`
	Password  string `json:"password,omitempty"`
	RootUser  string `json:"rootUser,omitempty"`
	RootPass  string `json:"rootPassword,omitempty"`
	Database  string `json:"database,omitempty"`
	Internal  string `json:"internalUrl"`
	PublicURL string `json:"publicUrl,omitempty"`
	Installed string `json:"installedAt"`
}

// Database is one logical database inside an instance.
type Database struct {
	Name        string `json:"name"`
	Size        string `json:"size"`
	Connections int    `json:"connections"`
	Owner       string `json:"owner,omitempty"`
}

// Stats is the health block for an instance.
type Stats struct {
	Version        string   `json:"version"`
	Connections    int      `json:"connections"`
	MaxConnections int      `json:"maxConnections"`
	Uptime         string   `json:"uptime"`
	DataSize       string   `json:"dataSize"`
	Extra          []string `json:"extra,omitempty"`
}

// Extension is a Postgres extension and whether it is installed.
type Extension struct {
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Available bool   `json:"available"`
	Comment   string `json:"comment"`
}

// SlowQuery comes from pg_stat_statements.
type SlowQuery struct {
	Query  string  `json:"query"`
	Calls  int64   `json:"calls"`
	MeanMs float64 `json:"meanMs"`
}

// Dump is a stored backup file.
type Dump struct {
	File      string `json:"file"`
	Database  string `json:"database"`
	Size      int64  `json:"size"`
	CreatedAt string `json:"createdAt"`
}

// Service is the database manager.
type Service struct {
	run     *cmdrun.Runner
	dk      *docker.Service
	cat     *catalog.Service
	dumpDir string
	stacks  string
}

// New builds the service.
func New(run *cmdrun.Runner, dk *docker.Service, cat *catalog.Service, dataDir string) *Service {
	abs, _ := filepath.Abs(dataDir)
	return &Service{run: run, dk: dk, cat: cat, dumpDir: filepath.Join(abs, "dumps"), stacks: filepath.Join(abs, "stacks")}
}

// ErrNotFound is returned for unknown instances or databases.
var ErrNotFound = errors.New("not found")

var identRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,62}$`)

// List returns every database instance with live state.
func (s *Service) List(ctx context.Context, actor string) ([]Instance, error) {
	apps, err := s.cat.InstalledApps()
	if err != nil {
		return nil, err
	}
	out := []Instance{}
	for _, a := range apps {
		eng, ok := engines[a.Slug]
		if !ok {
			continue
		}
		inst, err := s.build(ctx, actor, a, eng)
		if err != nil {
			continue
		}
		out = append(out, *inst)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Get returns one instance.
func (s *Service) Get(ctx context.Context, actor, name string) (*Instance, error) {
	apps, err := s.cat.InstalledApps()
	if err != nil {
		return nil, err
	}
	for _, a := range apps {
		if a.Name != name {
			continue
		}
		eng, ok := engines[a.Slug]
		if !ok {
			return nil, ErrNotFound
		}
		return s.build(ctx, actor, a, eng)
	}
	return nil, ErrNotFound
}

func (s *Service) build(ctx context.Context, actor string, a catalog.Installed, eng string) (*Instance, error) {
	app, err := s.cat.Get(a.Slug)
	if err != nil {
		return nil, err
	}
	v := a.Values
	inst := &Instance{Name: a.Name, Slug: a.Slug, Engine: eng, Port: app.Port, Container: a.Name + "-" + app.Service + "-1", Network: a.Name + "_default", Installed: a.InstalledAt}
	switch a.Slug {
	case "postgres":
		inst.User, inst.Password, inst.Database = v["POSTGRES_USER"], v["POSTGRES_PASSWORD"], v["POSTGRES_DB"]
		inst.RootUser, inst.RootPass = inst.User, inst.Password
	case "mysql":
		inst.User, inst.Password, inst.Database = v["MYSQL_USER"], v["MYSQL_PASSWORD"], v["MYSQL_DATABASE"]
		inst.RootUser, inst.RootPass = "root", v["MYSQL_ROOT_PASSWORD"]
	case "mariadb":
		inst.User, inst.Password, inst.Database = v["MARIADB_USER"], v["MARIADB_PASSWORD"], v["MARIADB_DATABASE"]
		inst.RootUser, inst.RootPass = "root", v["MARIADB_ROOT_PASSWORD"]
	case "redis", "valkey":
		inst.User, inst.Password = "default", v["REDIS_PASSWORD"]
		inst.RootUser, inst.RootPass = inst.User, inst.Password
	case "mongodb":
		inst.User, inst.Password = v["MONGO_USER"], v["MONGO_PASSWORD"]
		inst.RootUser, inst.RootPass = inst.User, inst.Password
	}
	if info, err := s.dk.Inspect(ctx, actor, inst.Container); err == nil {
		inst.State, inst.Image = info.State, info.Image
		for port, hosts := range info.Ports {
			if strings.HasPrefix(port, strconv.Itoa(inst.Port)+"/") && hosts != "" {
				h := strings.Split(hosts, ", ")[0]
				if strings.HasPrefix(h, "0.0.0.0:") || strings.HasPrefix(h, ":::") {
					if ip := proxy.PublicIP(ctx); ip != "" {
						h = ip + h[strings.LastIndex(h, ":"):]
					}
				}
				inst.Public = h
			}
		}
	} else {
		inst.State = "missing"
	}
	inst.Internal = s.connURL(inst, inst.Container, inst.Port, inst.User, inst.Password, inst.Database)
	if inst.Engine == "postgres" && s.HasPooler(inst) {
		inst.Pooler = true
		inst.Pooled = s.connURL(inst, inst.Name+"-pgbouncer-1", 5432, inst.User, inst.Password, inst.Database)
	}
	if inst.Public != "" {
		host, port, _ := strings.Cut(inst.Public, ":")
		p, _ := strconv.Atoi(port)
		inst.PublicURL = s.connURL(inst, host, p, inst.User, inst.Password, inst.Database)
	}
	return inst, nil
}

func (s *Service) connURL(inst *Instance, host string, port int, user, pass, db string) string {
	u := url.UserPassword(user, pass)
	switch inst.Engine {
	case "postgres":
		return fmt.Sprintf("postgres://%s@%s:%d/%s", u, host, port, db)
	case "mysql":
		return fmt.Sprintf("mysql://%s@%s:%d/%s", u, host, port, db)
	case "redis":
		return fmt.Sprintf("redis://:%s@%s:%d/0", url.QueryEscape(pass), host, port)
	case "mongo":
		return fmt.Sprintf("mongodb://%s@%s:%d/?authSource=admin", u, host, port)
	}
	return ""
}

// Redact clears secrets for non-admin viewers.
func (i *Instance) Redact() {
	i.Password, i.RootPass, i.Internal, i.PublicURL = "", "", "", ""
}

// exec runs a command inside the instance's container and returns stdout.
func (s *Service) exec(ctx context.Context, actor string, inst *Instance, stdin string, args ...string) (string, error) {
	full := append([]string{"exec", "-i", inst.Container}, args...)
	res, err := s.run.RunInput(ctx, actor, []byte(stdin), "docker", full...)
	if err != nil {
		var ce *cmdrun.Error
		if errors.As(err, &ce) {
			msg := strings.TrimSpace(ce.Result.Stderr)
			if msg == "" {
				msg = strings.TrimSpace(ce.Result.Stdout)
			}
			return "", errors.New(cleanErr(msg))
		}
		return "", err
	}
	return res.Stdout, nil
}

func cleanErr(msg string) string {
	msg = strings.ReplaceAll(msg, "mysql: [Warning] Using a password on the command line interface can be insecure.", "")
	msg = strings.TrimSpace(msg)
	if len(msg) > 400 {
		msg = msg[:400] + "…"
	}
	if msg == "" {
		return "command failed"
	}
	return msg
}

func (s *Service) sql(ctx context.Context, actor string, inst *Instance, database, query string) (string, error) {
	switch inst.Engine {
	case "postgres":
		if database == "" {
			database = "postgres"
		}
		return s.exec(ctx, actor, inst, query, "psql", "-U", inst.RootUser, "-d", database, "-v", "ON_ERROR_STOP=1", "-At", "-F", "\t", "-f", "-")
	case "mysql":
		args := []string{"mysql", "-uroot", "-p" + inst.RootPass, "-N", "-B"}
		if database != "" {
			args = append(args, database)
		}
		return s.exec(ctx, actor, inst, query, args...)
	}
	return "", errors.New("SQL is not supported for this engine")
}

func rowsOf(out string) [][]string {
	var rows [][]string
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		rows = append(rows, strings.Split(l, "\t"))
	}
	return rows
}

// Databases lists the logical databases inside an instance.
func (s *Service) Databases(ctx context.Context, actor string, inst *Instance) ([]Database, error) {
	out := []Database{}
	switch inst.Engine {
	case "postgres":
		res, err := s.sql(ctx, actor, inst, "", `SELECT d.datname, pg_size_pretty(pg_database_size(d.datname)), (SELECT count(*) FROM pg_stat_activity a WHERE a.datname = d.datname), pg_get_userbyid(d.datdba) FROM pg_database d WHERE NOT d.datistemplate ORDER BY 1;`)
		if err != nil {
			return nil, err
		}
		for _, r := range rowsOf(res) {
			if len(r) < 4 {
				continue
			}
			n, _ := strconv.Atoi(r[2])
			out = append(out, Database{Name: r[0], Size: r[1], Connections: n, Owner: r[3]})
		}
	case "mysql":
		res, err := s.sql(ctx, actor, inst, "", `SELECT s.schema_name, IFNULL(ROUND(SUM(t.data_length + t.index_length)/1024/1024, 1), 0), (SELECT COUNT(*) FROM information_schema.processlist p WHERE p.db = s.schema_name) FROM information_schema.schemata s LEFT JOIN information_schema.tables t ON t.table_schema = s.schema_name WHERE s.schema_name NOT IN ('information_schema','performance_schema','mysql','sys') GROUP BY s.schema_name ORDER BY 1;`)
		if err != nil {
			return nil, err
		}
		for _, r := range rowsOf(res) {
			if len(r) < 3 {
				continue
			}
			n, _ := strconv.Atoi(r[2])
			out = append(out, Database{Name: r[0], Size: r[1] + " MB", Connections: n})
		}
	case "redis":
		res, err := s.exec(ctx, actor, inst, "", "redis-cli", "-a", inst.RootPass, "--no-auth-warning", "INFO", "keyspace")
		if err != nil {
			return nil, err
		}
		for _, l := range strings.Split(res, "\n") {
			l = strings.TrimSpace(l)
			if strings.HasPrefix(l, "db") {
				name, rest, _ := strings.Cut(l, ":")
				keys := strings.SplitN(rest, ",", 2)[0]
				out = append(out, Database{Name: name, Size: strings.TrimPrefix(keys, "keys=") + " keys"})
			}
		}
	case "mongo":
		res, err := s.exec(ctx, actor, inst, "", "mongosh", "--quiet", "-u", inst.RootUser, "-p", inst.RootPass, "--authenticationDatabase", "admin", "--eval", `db.adminCommand("listDatabases").databases.forEach(d => print(d.name + "\t" + d.sizeOnDisk))`)
		if err != nil {
			return nil, err
		}
		for _, r := range rowsOf(res) {
			if len(r) < 2 {
				continue
			}
			b, _ := strconv.ParseInt(r[1], 10, 64)
			out = append(out, Database{Name: r[0], Size: human(b)})
		}
	}
	return out, nil
}

// CreateDatabase creates a database and an owning user, returning the user's connection URL.
func (s *Service) CreateDatabase(ctx context.Context, actor string, inst *Instance, name, user, password string) (string, error) {
	if !identRe.MatchString(name) || (user != "" && !identRe.MatchString(user)) {
		return "", errors.New("names must be letters, digits and underscores, starting with a letter")
	}
	if user == "" {
		user = name
	}
	if password == "" {
		password = randomSecret(24)
	}
	switch inst.Engine {
	case "postgres":
		q := fmt.Sprintf("CREATE USER %q WITH PASSWORD %s;\nCREATE DATABASE %q OWNER %q;\n", user, pgQuote(password), name, user)
		if user == inst.RootUser {
			q = fmt.Sprintf("CREATE DATABASE %q OWNER %q;\n", name, user)
			password = inst.RootPass
		}
		if _, err := s.sql(ctx, actor, inst, "", q); err != nil {
			return "", err
		}
	case "mysql":
		q := fmt.Sprintf("CREATE DATABASE `%s`;\nCREATE USER '%s'@'%%' IDENTIFIED BY %s;\nGRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'%%';\nFLUSH PRIVILEGES;\n", name, user, pgQuote(password), name, user)
		if _, err := s.sql(ctx, actor, inst, "", q); err != nil {
			return "", err
		}
	case "mongo":
		js := fmt.Sprintf(`db.getSiblingDB(%q).createUser({user: %q, pwd: %q, roles: [{role: "readWrite", db: %q}, {role: "dbAdmin", db: %q}]})`, name, user, password, name, name)
		if _, err := s.exec(ctx, actor, inst, "", "mongosh", "--quiet", "-u", inst.RootUser, "-p", inst.RootPass, "--authenticationDatabase", "admin", "--eval", js); err != nil {
			return "", err
		}
		return fmt.Sprintf("mongodb://%s@%s:%d/%s", url.UserPassword(user, password), inst.Container, inst.Port, name), nil
	default:
		return "", errors.New("this engine has no separate databases")
	}
	return s.connURL(inst, inst.Container, inst.Port, user, password, name), nil
}

// DropDatabase removes a database (and its same-named user when one exists).
func (s *Service) DropDatabase(ctx context.Context, actor string, inst *Instance, name string) error {
	if !identRe.MatchString(name) {
		return errors.New("invalid database name")
	}
	if name == inst.Database || name == "postgres" || name == "mysql" || name == "admin" || name == "local" || name == "config" {
		return errors.New("that database is the instance's primary or a system database; remove the whole instance instead")
	}
	switch inst.Engine {
	case "postgres":
		_, err := s.sql(ctx, actor, inst, "", fmt.Sprintf("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = %s;\nDROP DATABASE %q;\nDROP ROLE IF EXISTS %q;\n", pgQuote(name), name, name))
		return err
	case "mysql":
		_, err := s.sql(ctx, actor, inst, "", fmt.Sprintf("DROP DATABASE `%s`;\nDROP USER IF EXISTS '%s'@'%%';\n", name, name))
		return err
	case "mongo":
		_, err := s.exec(ctx, actor, inst, "", "mongosh", "--quiet", "-u", inst.RootUser, "-p", inst.RootPass, "--authenticationDatabase", "admin", "--eval", fmt.Sprintf(`db.getSiblingDB(%q).dropDatabase()`, name))
		return err
	}
	return errors.New("this engine has no separate databases")
}

// Stats returns version, connections and size.
func (s *Service) Stats(ctx context.Context, actor string, inst *Instance) (*Stats, error) {
	st := &Stats{}
	switch inst.Engine {
	case "postgres":
		res, err := s.sql(ctx, actor, inst, "", `SELECT version(), (SELECT count(*) FROM pg_stat_activity), current_setting('max_connections'), date_trunc('second', now() - pg_postmaster_start_time()), pg_size_pretty(sum(pg_database_size(datname))) FROM pg_database;`)
		if err != nil {
			return nil, err
		}
		if r := rowsOf(res); len(r) == 1 && len(r[0]) >= 5 {
			st.Version = strings.Join(strings.Fields(r[0][0])[:2], " ")
			st.Connections, _ = strconv.Atoi(r[0][1])
			st.MaxConnections, _ = strconv.Atoi(r[0][2])
			st.Uptime, st.DataSize = r[0][3], r[0][4]
		}
	case "mysql":
		res, err := s.sql(ctx, actor, inst, "", `SELECT VERSION(), (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Threads_connected'), @@max_connections, (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Uptime'), (SELECT IFNULL(ROUND(SUM(data_length+index_length)/1024/1024,1),0) FROM information_schema.tables);`)
		if err != nil {
			return nil, err
		}
		if r := rowsOf(res); len(r) == 1 && len(r[0]) >= 5 {
			st.Version = "MySQL " + r[0][0]
			if inst.Slug == "mariadb" {
				st.Version = "MariaDB " + r[0][0]
			}
			st.Connections, _ = strconv.Atoi(r[0][1])
			st.MaxConnections, _ = strconv.Atoi(r[0][2])
			secs, _ := strconv.Atoi(r[0][3])
			st.Uptime = (time.Duration(secs) * time.Second).String()
			st.DataSize = r[0][4] + " MB"
		}
	case "redis":
		res, err := s.exec(ctx, actor, inst, "", "redis-cli", "-a", inst.RootPass, "--no-auth-warning", "INFO")
		if err != nil {
			return nil, err
		}
		kv := map[string]string{}
		for _, l := range strings.Split(res, "\n") {
			if k, v, ok := strings.Cut(strings.TrimSpace(l), ":"); ok {
				kv[k] = v
			}
		}
		st.Version = "Redis " + kv["redis_version"]
		st.Connections, _ = strconv.Atoi(kv["connected_clients"])
		st.MaxConnections, _ = strconv.Atoi(kv["maxclients"])
		secs, _ := strconv.Atoi(kv["uptime_in_seconds"])
		st.Uptime = (time.Duration(secs) * time.Second).String()
		st.DataSize = kv["used_memory_human"]
		st.Extra = []string{"hit rate: " + hitRate(kv["keyspace_hits"], kv["keyspace_misses"]), "evicted keys: " + kv["evicted_keys"], "persistence: " + map[string]string{"1": "AOF on", "0": "AOF off"}[kv["aof_enabled"]]}
	case "mongo":
		res, err := s.exec(ctx, actor, inst, "", "mongosh", "--quiet", "-u", inst.RootUser, "-p", inst.RootPass, "--authenticationDatabase", "admin", "--eval", `const s = db.serverStatus(); print(s.version + "\t" + s.connections.current + "\t" + s.connections.available + "\t" + s.uptime)`)
		if err != nil {
			return nil, err
		}
		if r := rowsOf(res); len(r) == 1 && len(r[0]) >= 4 {
			st.Version = "MongoDB " + r[0][0]
			st.Connections, _ = strconv.Atoi(r[0][1])
			avail, _ := strconv.Atoi(r[0][2])
			st.MaxConnections = st.Connections + avail
			secs, _ := strconv.Atoi(r[0][3])
			st.Uptime = (time.Duration(secs) * time.Second).String()
		}
	}
	return st, nil
}

func hitRate(hits, misses string) string {
	h, _ := strconv.ParseFloat(hits, 64)
	m, _ := strconv.ParseFloat(misses, 64)
	if h+m == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.0f%%", h/(h+m)*100)
}

var knownExtensions = []string{"pg_stat_statements", "pg_trgm", "pgcrypto", "uuid-ossp", "hstore", "citext", "vector", "postgis", "timescaledb"}

// Extensions lists well-known Postgres extensions and their state in the primary database.
func (s *Service) Extensions(ctx context.Context, actor string, inst *Instance) ([]Extension, error) {
	if inst.Engine != "postgres" {
		return []Extension{}, nil
	}
	res, err := s.sql(ctx, actor, inst, inst.Database, `SELECT name, COALESCE(installed_version, ''), COALESCE(comment, '') FROM pg_available_extensions;`)
	if err != nil {
		return nil, err
	}
	avail := map[string][2]string{}
	for _, r := range rowsOf(res) {
		if len(r) >= 3 {
			avail[r[0]] = [2]string{r[1], r[2]}
		}
	}
	out := []Extension{}
	for _, n := range knownExtensions {
		a, ok := avail[n]
		e := Extension{Name: n, Available: ok, Installed: ok && a[0] != "", Comment: a[1]}
		if !ok {
			e.Comment = map[string]string{"vector": "needs the pgvector/pgvector image", "postgis": "needs the postgis/postgis image", "timescaledb": "needs the timescale/timescaledb image"}[n]
		}
		out = append(out, e)
	}
	return out, nil
}

// SetExtension creates or drops an extension in the primary database.
func (s *Service) SetExtension(ctx context.Context, actor string, inst *Instance, name string, on bool) error {
	ok := false
	for _, n := range knownExtensions {
		if n == name {
			ok = true
		}
	}
	if !ok || inst.Engine != "postgres" {
		return errors.New("unknown extension")
	}
	q := fmt.Sprintf("DROP EXTENSION IF EXISTS %q;", name)
	if on {
		q = fmt.Sprintf("CREATE EXTENSION IF NOT EXISTS %q;", name)
	}
	_, err := s.sql(ctx, actor, inst, inst.Database, q)
	if err == nil && name == "pg_stat_statements" && on {
		// The library must be preloaded for the view to collect data.
		_, _ = s.sql(ctx, actor, inst, "", "ALTER SYSTEM SET shared_preload_libraries = 'pg_stat_statements';")
		return errors.New("extension created; restart the instance so shared_preload_libraries takes effect")
	}
	return err
}

// SlowQueries reads pg_stat_statements when it is enabled.
func (s *Service) SlowQueries(ctx context.Context, actor string, inst *Instance) ([]SlowQuery, error) {
	if inst.Engine != "postgres" {
		return []SlowQuery{}, nil
	}
	res, err := s.sql(ctx, actor, inst, inst.Database, `SELECT left(regexp_replace(query, '\s+', ' ', 'g'), 200), calls, round(mean_exec_time::numeric, 2) FROM pg_stat_statements WHERE query NOT ILIKE '%pg_stat_statements%' ORDER BY mean_exec_time DESC LIMIT 15;`)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") {
			return []SlowQuery{}, nil
		}
		return nil, err
	}
	out := []SlowQuery{}
	for _, r := range rowsOf(res) {
		if len(r) < 3 {
			continue
		}
		calls, _ := strconv.ParseInt(r[1], 10, 64)
		ms, _ := strconv.ParseFloat(r[2], 64)
		out = append(out, SlowQuery{Query: r[0], Calls: calls, MeanMs: ms})
	}
	return out, nil
}

// ---- dumps ----

func (s *Service) instDumpDir(inst *Instance) string { return filepath.Join(s.dumpDir, inst.Name) }

// Dumps lists stored dump files, newest first.
func (s *Service) Dumps(inst *Instance) ([]Dump, error) {
	entries, err := os.ReadDir(s.instDumpDir(inst))
	if errors.Is(err, os.ErrNotExist) {
		return []Dump{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Dump{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		name := e.Name()
		dbName := name
		if i := strings.LastIndex(name, "-20"); i > 0 {
			dbName = name[:i]
		}
		out = append(out, Dump{File: name, Database: dbName, Size: info.Size(), CreatedAt: info.ModTime().UTC().Format(time.RFC3339)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

// DumpPath validates a file name and returns its full path.
func (s *Service) DumpPath(inst *Instance, file string) (string, error) {
	if file == "" || strings.ContainsAny(file, `/\`) || strings.HasPrefix(file, ".") {
		return "", errors.New("invalid dump name")
	}
	p := filepath.Join(s.instDumpDir(inst), file)
	if _, err := os.Stat(p); err != nil {
		return "", ErrNotFound
	}
	return p, nil
}

// dumpArgv is the in-container command whose stdout is the dump.
func dumpArgv(inst *Instance, database string) ([]string, string, error) {
	switch inst.Engine {
	case "postgres":
		return []string{"pg_dump", "-U", inst.RootUser, "--no-owner", "--clean", "--if-exists", database}, ".sql.gz", nil
	case "mysql":
		return []string{"mysqldump", "-uroot", "-p" + inst.RootPass, "--single-transaction", "--routines", "--triggers", database}, ".sql.gz", nil
	case "mongo":
		return []string{"mongodump", "-u", inst.RootUser, "-p", inst.RootPass, "--authenticationDatabase", "admin", "--db", database, "--archive"}, ".archive.gz", nil
	case "redis":
		return []string{"sh", "-c", `redis-cli -a "$REDIS_PASSWORD" --no-auth-warning --rdb /tmp/islet-dump.rdb >/dev/null 2>&1 && cat /tmp/islet-dump.rdb && rm -f /tmp/islet-dump.rdb`}, ".rdb.gz", nil
	}
	return nil, "", errors.New("dumps are not supported for this engine")
}

// DumpNow writes a gzipped dump of one database and returns the file name.
func (s *Service) DumpNow(ctx context.Context, actor string, inst *Instance, database string) (*Dump, error) {
	if inst.Engine == "redis" {
		database = "redis"
	}
	if !identRe.MatchString(database) {
		return nil, errors.New("invalid database name")
	}
	argv, ext, err := dumpArgv(inst, database)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.instDumpDir(inst), 0o750); err != nil {
		return nil, err
	}
	name := fmt.Sprintf("%s-%s%s", database, time.Now().UTC().Format("20060102-150405"), ext)
	dst := filepath.Join(s.instDumpDir(inst), name)
	f, err := os.OpenFile(dst+".part", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	gz := gzip.NewWriter(f)
	full := append([]string{"exec", "-e", "REDIS_PASSWORD=" + inst.RootPass, inst.Container}, argv...)
	cmd := exec.CommandContext(ctx, "docker", full...)
	cmd.Stdout = gz
	var stderr strings.Builder
	cmd.Stderr = &stderr
	start := time.Now()
	runErr := cmd.Run()
	_ = gz.Close()
	_ = f.Close()
	s.record(ctx, actor, "docker exec "+inst.Container+" "+argv[0]+" …", runErr, time.Since(start))
	if runErr != nil {
		os.Remove(dst + ".part")
		return nil, errors.New(cleanErr(stderr.String()))
	}
	if err := os.Rename(dst+".part", dst); err != nil {
		return nil, err
	}
	info, _ := os.Stat(dst)
	return &Dump{File: name, Database: database, Size: info.Size(), CreatedAt: info.ModTime().UTC().Format(time.RFC3339)}, nil
}

// Restore loads a dump into a database (created when missing for SQL engines).
func (s *Service) Restore(ctx context.Context, actor string, inst *Instance, file, database string) error {
	p, err := s.DumpPath(inst, file)
	if err != nil {
		return err
	}
	if inst.Engine != "redis" && !identRe.MatchString(database) {
		return errors.New("invalid database name")
	}
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("not a gzip dump: %w", err)
	}
	var argv []string
	switch inst.Engine {
	case "postgres":
		if _, err := s.sql(ctx, actor, inst, "", fmt.Sprintf("SELECT 1 FROM pg_database WHERE datname = %s;", pgQuote(database))); err == nil {
			if out, _ := s.sql(ctx, actor, inst, "", fmt.Sprintf("SELECT 1 FROM pg_database WHERE datname = %s;", pgQuote(database))); strings.TrimSpace(out) == "" {
				if _, err := s.sql(ctx, actor, inst, "", fmt.Sprintf("CREATE DATABASE %q;", database)); err != nil {
					return err
				}
			}
		}
		argv = []string{"psql", "-U", inst.RootUser, "-d", database, "-v", "ON_ERROR_STOP=0", "-q"}
	case "mysql":
		if _, err := s.sql(ctx, actor, inst, "", fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s`;", database)); err != nil {
			return err
		}
		argv = []string{"mysql", "-uroot", "-p" + inst.RootPass, database}
	case "mongo":
		argv = []string{"mongorestore", "-u", inst.RootUser, "-p", inst.RootPass, "--authenticationDatabase", "admin", "--archive", "--drop", "--nsFrom", database + ".*", "--nsTo", database + ".*"}
	case "redis":
		return errors.New("restore a Redis dump by stopping the instance and replacing dump.rdb in its data volume; Islet keeps the file under " + p)
	}
	full := append([]string{"exec", "-i", inst.Container}, argv...)
	cmd := exec.CommandContext(ctx, "docker", full...)
	cmd.Stdin = gz
	var stderr strings.Builder
	cmd.Stderr = &stderr
	start := time.Now()
	runErr := cmd.Run()
	s.record(ctx, actor, "docker exec -i "+inst.Container+" "+argv[0]+" < "+file, runErr, time.Since(start))
	if runErr != nil {
		return errors.New(cleanErr(stderr.String()))
	}
	return nil
}

// DeleteDump removes a stored dump.
func (s *Service) DeleteDump(inst *Instance, file string) error {
	p, err := s.DumpPath(inst, file)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

// DumpScript renders a cron script that dumps every database of an instance
// and prunes old files, so scheduled dumps are ordinary jobs.
func (s *Service) DumpScript(inst *Instance, keepDays int) string {
	dir := filepath.ToSlash(s.instDumpDir(inst))
	var body string
	switch inst.Engine {
	case "postgres":
		body = fmt.Sprintf(`for db in $(docker exec %s psql -U %s -At -c "SELECT datname FROM pg_database WHERE NOT datistemplate AND datname <> 'postgres'"); do
  docker exec %s pg_dump -U %s --no-owner --clean --if-exists "$db" | gzip -6 > "$DIR/$db-$STAMP.sql.gz"
  echo "dumped $db"
done`, inst.Container, inst.RootUser, inst.Container, inst.RootUser)
	case "mysql":
		body = fmt.Sprintf(`for db in $(docker exec %s mysql -uroot -p"$ROOT_PW" -N -B -e "SHOW DATABASES" | grep -Ev '^(information_schema|performance_schema|mysql|sys)$'); do
  docker exec %s mysqldump -uroot -p"$ROOT_PW" --single-transaction --routines --triggers "$db" | gzip -6 > "$DIR/$db-$STAMP.sql.gz"
  echo "dumped $db"
done`, inst.Container, inst.Container)
	case "mongo":
		body = fmt.Sprintf(`docker exec %s mongodump -u %s -p "$ROOT_PW" --authenticationDatabase admin --archive | gzip -6 > "$DIR/all-$STAMP.archive.gz"
echo "dumped all databases"`, inst.Container, inst.RootUser)
	case "redis":
		body = fmt.Sprintf(`docker exec %s sh -c 'redis-cli -a "%s" --no-auth-warning --rdb /tmp/islet-dump.rdb >/dev/null 2>&1 && cat /tmp/islet-dump.rdb && rm -f /tmp/islet-dump.rdb' | gzip -6 > "$DIR/redis-$STAMP.rdb.gz"
echo "dumped redis"`, inst.Container, inst.RootPass)
	}
	return fmt.Sprintf(`#!/usr/bin/env bash
# Generated by Islet for the %s instance "%s". Edit freely; Islet will not overwrite it.
set -euo pipefail
DIR=%q
KEEP_DAYS=%d
ROOT_PW=%q
STAMP=$(date +%%Y%%m%%d-%%H%%M%%S)
mkdir -p "$DIR"

%s

find "$DIR" -type f -mtime +$KEEP_DAYS -delete
echo "kept $(ls -1 "$DIR" | wc -l) files, older than $KEEP_DAYS days removed"
`, inst.Engine, inst.Name, dir, keepDays, inst.RootPass, body)
}

// ---- exposure ----

// SetPublic publishes or unpublishes the instance port on the host by
// editing the stack's Compose file and recreating the service. hostPort
// defaults to the engine port; pick another when it is taken.
func (s *Service) SetPublic(ctx context.Context, actor string, inst *Instance, on bool, hostPort int) (io.ReadCloser, func() error, error) {
	if hostPort <= 0 {
		hostPort = inst.Port
	}
	if hostPort < 1024 || hostPort > 65535 {
		return nil, nil, errors.New("host port must be between 1024 and 65535")
	}
	compose, env, err := s.dk.ReadStack(inst.Name)
	if err != nil {
		return nil, nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(compose), &doc); err != nil {
		return nil, nil, err
	}
	svcName := strings.TrimSuffix(strings.TrimPrefix(inst.Container, inst.Name+"-"), "-1")
	svc := mapGet(mapGet(doc.Content[0], "services"), svcName)
	if svc == nil {
		return nil, nil, errors.New("service not found in compose file")
	}
	mapDel(svc, "ports")
	if on {
		ports := &yaml.Node{Kind: yaml.SequenceNode}
		ports.Content = append(ports.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%d:%d", hostPort, inst.Port), Style: yaml.DoubleQuotedStyle})
		svc.Content = append(svc.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "ports"}, ports)
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, nil, err
	}
	if err := s.dk.WriteStack(ctx, actor, inst.Name, string(out), env); err != nil {
		return nil, nil, err
	}
	return s.dk.StackAction(ctx, actor, inst.Name, "up")
}

func mapGet(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func mapDel(n *yaml.Node, key string) {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			n.Content = append(n.Content[:i], n.Content[i+2:]...)
			return
		}
	}
}

// ---- helpers ----

func (s *Service) record(ctx context.Context, actor, desc string, err error, d time.Duration) {
	res := cmdrun.Result{Duration: d}
	if err != nil {
		res.ExitCode, res.Stderr = 1, err.Error()
	}
	s.run.Record(ctx, actor, desc, res)
}

func pgQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func human(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	v := float64(n)
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", v), ".0") + " " + units[i]
}

func randomSecret(n int) string {
	const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	for i := range buf {
		buf[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return string(buf)
}
