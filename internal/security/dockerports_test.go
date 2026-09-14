package security

import (
	"reflect"
	"testing"
)

func TestPublishedDBPorts(t *testing.T) {
	cases := []struct {
		name  string
		ports string
		want  []string
	}{{
		// The case that prompted this: the host port is 5433, not 5432, and the
		// old check reported 5432 — a port nobody had published.
		name:  "host port differs from container port",
		ports: "0.0.0.0:5433->5432/tcp, [::]:5433->5432/tcp",
		want:  []string{"5433 -> 5432 (PostgreSQL)"},
	}, {
		// The false positive: a database on loopback only, alongside an
		// unrelated public port. The old condition saw "0.0.0.0:" and
		// "->5432/" anywhere in the string and called it exposed.
		name:  "loopback database next to a public web port",
		ports: "127.0.0.1:5432->5432/tcp, 0.0.0.0:8080->80/tcp",
		want:  nil,
	}, {
		name:  "exposed by the image but never published",
		ports: "5432/tcp",
		want:  nil,
	}, {
		name:  "published on every interface",
		ports: "0.0.0.0:5432->5432/tcp, [::]:5432->5432/tcp",
		want:  []string{"5432 (PostgreSQL)"},
	}, {
		name:  "a single address is not the internet",
		ports: "10.0.0.5:6379->6379/tcp",
		want:  nil,
	}, {
		name:  "several databases in one container",
		ports: "0.0.0.0:3306->3306/tcp, 0.0.0.0:6379->6379/tcp",
		want:  []string{"3306 (MySQL)", "6379 (Redis)"},
	}, {
		name:  "not a database port",
		ports: "0.0.0.0:8080->80/tcp, 0.0.0.0:443->443/tcp",
		want:  nil,
	}, {
		name:  "nothing published at all",
		ports: "",
		want:  nil,
	}, {
		name:  "mongo on a shifted host port",
		ports: "0.0.0.0:27018->27017/tcp",
		want:  []string{"27018 -> 27017 (MongoDB)"},
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := PublishedDBPorts(c.ports)
			if len(got) == 0 && len(c.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("PublishedDBPorts(%q)\n got %q\nwant %q", c.ports, got, c.want)
			}
		})
	}
}
