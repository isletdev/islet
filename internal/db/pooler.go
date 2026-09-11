package db

import (
	"context"
	"errors"
	"io"

	"gopkg.in/yaml.v3"
)

// poolerImage is the PgBouncer image used for the connection pooler.
const poolerImage = "edoburu/pgbouncer:v1.24.1-p1"

// HasPooler reports whether the instance's stack defines the pgbouncer service.
func (s *Service) HasPooler(inst *Instance) bool {
	compose, _, err := s.dk.ReadStack(inst.Name)
	if err != nil {
		return false
	}
	var doc yaml.Node
	if yaml.Unmarshal([]byte(compose), &doc) != nil || len(doc.Content) == 0 {
		return false
	}
	return mapGet(mapGet(doc.Content[0], "services"), "pgbouncer") != nil
}

// SetPooler adds or removes a PgBouncer service (transaction pooling) in
// front of a Postgres instance. Apps keep the same credentials and swap the
// host for <name>-pgbouncer-1.
func (s *Service) SetPooler(ctx context.Context, actor string, inst *Instance, on bool) (io.ReadCloser, func() error, error) {
	if inst.Engine != "postgres" {
		return nil, nil, errors.New("PgBouncer only pools PostgreSQL")
	}
	compose, env, err := s.dk.ReadStack(inst.Name)
	if err != nil {
		return nil, nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(compose), &doc); err != nil {
		return nil, nil, err
	}
	services := mapGet(doc.Content[0], "services")
	if services == nil {
		return nil, nil, errors.New("services not found in compose file")
	}
	mapDel(services, "pgbouncer")
	if on {
		var svc yaml.Node
		src := `image: ` + poolerImage + `
restart: unless-stopped
depends_on:
  - db
environment:
  DB_HOST: db
  DB_PORT: "5432"
  DB_USER: ${POSTGRES_USER}
  DB_PASSWORD: ${POSTGRES_PASSWORD}
  AUTH_TYPE: scram-sha-256
  POOL_MODE: transaction
  MAX_CLIENT_CONN: "1000"
  DEFAULT_POOL_SIZE: "20"
  MAX_DB_CONNECTIONS: "50"
  IGNORE_STARTUP_PARAMETERS: extra_float_digits
`
		if err := yaml.Unmarshal([]byte(src), &svc); err != nil {
			return nil, nil, err
		}
		services.Content = append(services.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "pgbouncer"}, svc.Content[0])
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, nil, err
	}
	if err := s.dk.WriteStack(ctx, actor, inst.Name, string(out), env); err != nil {
		return nil, nil, err
	}
	if !on {
		// "up" does not remove services that left the file; drop the container first.
		_, _ = s.run.Run(ctx, actor, "docker", "rm", "-f", inst.Name+"-pgbouncer-1")
	}
	return s.dk.StackAction(ctx, actor, inst.Name, "up")
}
