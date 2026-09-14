package security

import (
	"sort"
	"strings"
)

// dbPorts are container ports that mean a database is listening, mapped to the
// name a person would recognise in the score.
var dbPorts = map[string]string{
	"5432":  "PostgreSQL",
	"3306":  "MySQL",
	"6379":  "Redis",
	"27017": "MongoDB",
	"9200":  "Elasticsearch",
	"5984":  "CouchDB",
	"8086":  "InfluxDB",
}

// publicBind reports whether a host address in a Docker port mapping accepts
// traffic from outside the machine. Docker writes the wildcard as 0.0.0.0 for
// IPv4 and [::] for IPv6, and omits the address entirely in some versions.
func publicBind(ip string) bool {
	switch strings.Trim(ip, "[]") {
	case "0.0.0.0", "::", "":
		return true
	}
	return false
}

// PublishedDBPorts reports the database ports a container publishes on a public
// interface, given one container's `docker ps {{.Ports}}` field.
//
// Docker prints one mapping per published port, "0.0.0.0:5433->5432/tcp": the
// host port is what the internet reaches, the container port identifies the
// database. They are frequently different. Testing for the two independently —
// "does this string contain 0.0.0.0" and "does it contain ->5432/" — reports a
// database as exposed when a wholly unrelated port is the public one, and names
// the container port, which nobody can connect to. Each mapping is parsed as a
// unit here so the port in the message is the port to close.
func PublishedDBPorts(ports string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range strings.Split(ports, ",") {
		m = strings.TrimSpace(m)
		host, container, ok := strings.Cut(m, "->")
		if !ok {
			continue // exposed by the image but not published to the host
		}
		cport, _, _ := strings.Cut(container, "/")
		name, isDB := dbPorts[cport]
		if !isDB {
			continue
		}
		i := strings.LastIndex(host, ":")
		if i < 0 {
			continue
		}
		if !publicBind(host[:i]) {
			continue // bound to loopback or one address: not the internet
		}
		hport := host[i+1:]
		if hport == "" || seen[hport] {
			continue // the same port over IPv4 and IPv6 is one exposure
		}
		seen[hport] = true
		if hport == cport {
			out = append(out, hport+" ("+name+")")
		} else {
			out = append(out, hport+" -> "+cport+" ("+name+")")
		}
	}
	sort.Strings(out)
	return out
}
