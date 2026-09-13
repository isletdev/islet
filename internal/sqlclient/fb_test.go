package sqlclient

import (
	"context"
	"errors"
	"testing"
	"time"
)

type pickyDriver struct {
	okHost string
	tried  []string
}

func (d *pickyDriver) Open(_ context.Context, cfg Config) (Conn, error) {
	d.tried = append(d.tried, cfg.Host)
	if cfg.Host != d.okHost {
		return nil, errors.New("could not reach " + cfg.Host)
	}
	return &fakeConn{drv: newFakeDriver(), cancelled: make(chan struct{})}, nil
}

func TestFallbackAddressIsTried(t *testing.T) {
	d := &pickyDriver{okHost: "127.0.0.1"}
	p := newPool(Config{Engine: "postgres", Host: "192.168.32.2", Port: 5432,
		FallbackHost: "127.0.0.1", FallbackPort: 15499}, d, time.Now)
	c, err := p.acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire: %v (tried %v)", err, d.tried)
	}
	if c == nil {
		t.Fatal("no connection")
	}
	if len(d.tried) != 2 || d.tried[0] != "192.168.32.2" || d.tried[1] != "127.0.0.1" {
		t.Errorf("tried %v, want the bridge address then the published one", d.tried)
	}
}
