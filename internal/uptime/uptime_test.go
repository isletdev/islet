package uptime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// This package decides whether to wake somebody up, so the two things worth
// pinning are what it accepts and what it calls "down".
func TestValidateFillsTheGapsAndRefusesTheRest(t *testing.T) {
	ok := &Check{Name: "shop", Type: "http", Target: "https://shop.example.com"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("an ordinary check was refused: %v", err)
	}
	// Defaults, so a check made from three fields is a working check.
	if ok.IntervalSec != 60 || ok.TimeoutSec != 10 {
		t.Errorf("defaults not applied: every %ds, timeout %ds", ok.IntervalSec, ok.TimeoutSec)
	}

	for _, c := range []struct {
		name  string
		check Check
		want  string
	}{
		{"no name", Check{Type: "http", Target: "https://x.example"}, "name"},
		{"a bare hostname is not a URL", Check{Name: "a", Type: "http", Target: "shop.example.com"}, "http://"},
		{"nor is a scheme this cannot probe", Check{Name: "a", Type: "http", Target: "ftp://x.example"}, "http://"},
		{"a keyword check without a keyword", Check{Name: "a", Type: "keyword", Target: "https://x.example"}, "keyword"},
		{"tcp wants a port", Check{Name: "a", Type: "tcp", Target: "db.example.com"}, "host:port"},
		{"an unknown type", Check{Name: "a", Type: "ping", Target: "x"}, "type must be"},
		// Ten seconds apart is a denial of service against your own site, and
		// a day and a half is not monitoring.
		{"too often", Check{Name: "a", Type: "http", Target: "https://x.example", IntervalSec: 10}, "interval"},
		{"too rarely", Check{Name: "a", Type: "http", Target: "https://x.example", IntervalSec: 200000}, "interval"},
		{"a timeout longer than a minute", Check{Name: "a", Type: "http", Target: "https://x.example", TimeoutSec: 90}, "timeout"},
		{"not an HTTP status", Check{Name: "a", Type: "http", Target: "https://x.example", ExpectStatus: 42}, "status"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := c.check.Validate()
			if err == nil {
				t.Fatalf("accepted: %+v", c.check)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("refused for the wrong reason: %v", err)
			}
		})
	}
}

// What counts as up. Each of these has a plausible wrong answer: a 500 that
// reads as fine, a keyword check that passes on an error page, an expected
// status that is ignored.
func TestWhatCountsAsDown(t *testing.T) {
	s := &Service{client: &http.Client{Timeout: 5 * time.Second}}

	body := "ok: the shop is open"
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("the probe should say who it is")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	up := func(c *Check) Result { return s.run(context.Background(), c) }

	c := &Check{Name: "shop", Type: "http", Target: srv.URL, TimeoutSec: 5}
	if r := up(c); !r.OK {
		t.Errorf("a 200 read as down: %s", r.Error)
	}

	status = 503
	if r := up(c); r.OK {
		t.Error("a 503 read as up")
	} else if !strings.Contains(r.Error, "503") {
		t.Errorf("the reason does not say what happened: %q", r.Error)
	}

	// A status somebody expects on purpose — a health endpoint that answers
	// 418, a login that answers 401 — is up when it answers that and down when
	// it answers 200.
	status = 401
	c.ExpectStatus = 401
	if r := up(c); !r.OK {
		t.Errorf("an expected 401 read as down: %s", r.Error)
	}
	status = 200
	if r := up(c); r.OK {
		t.Error("a 200 read as up for a check that expects 401")
	}
	c.ExpectStatus = 0

	// A keyword check is the one that catches a page that is serving, and
	// serving the wrong thing.
	status = 200
	c.Type, c.Keyword = "keyword", "the shop is open"
	if r := up(c); !r.OK {
		t.Errorf("the keyword was there and the check failed: %s", r.Error)
	}
	body = "Error 500 — something went wrong"
	if r := up(c); r.OK {
		t.Error("a page serving an error message read as up because it was HTTP 200")
	} else if !strings.Contains(r.Error, "keyword") {
		t.Errorf("the reason should name the keyword: %q", r.Error)
	}
}

// A target that never answers must fail as a timeout, in words, rather than as
// whatever the transport happened to say.
func TestATimeoutSaysSo(t *testing.T) {
	s := &Service{client: &http.Client{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	r := s.run(context.Background(), &Check{Name: "slow", Type: "http", Target: srv.URL, TimeoutSec: 1})
	if r.OK {
		t.Fatal("a probe that never answered read as up")
	}
	if !strings.Contains(r.Error, "no answer within 1s") {
		t.Errorf("a timeout should say so plainly, not %q", r.Error)
	}
}

// A TCP check is up when something accepts, down when nothing does.
func TestTCPProbe(t *testing.T) {
	s := &Service{}
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	if r := s.run(context.Background(), &Check{Name: "db", Type: "tcp", Target: host, TimeoutSec: 5}); !r.OK {
		t.Errorf("a listening port read as down: %s", r.Error)
	}
	// Port 1 on loopback: nothing listens there, and nothing should.
	if r := s.run(context.Background(), &Check{Name: "db", Type: "tcp", Target: "127.0.0.1:1", TimeoutSec: 5}); r.OK {
		t.Error("a closed port read as up")
	}
}
