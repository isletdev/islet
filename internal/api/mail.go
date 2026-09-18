package api

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/catalog"
	"github.com/isletdev/islet/internal/proxy"
	"github.com/isletdev/islet/pkg/api"
)

const relayStack = "mail"

// MailRelay is the outbound relay configuration plus the DNS records the
// domain needs, each with its current state.
type MailRelay struct {
	Domain    string      `json:"domain"`
	Hostname  string      `json:"hostname"`
	Relayhost string      `json:"relayhost,omitempty"`
	Installed bool        `json:"installed"`
	Running   bool        `json:"running"`
	PublicIP  string      `json:"publicIp"`
	PanelSMTP string      `json:"panelSmtp"` // host:port the panel's email channel can use
	AppSMTP   string      `json:"appSmtp"`   // host:port containers can use
	Records   []DNSRecord `json:"records"`
}

// DNSRecord is one record to publish and whether it is there.
type DNSRecord struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Value   string `json:"value"`
	Found   string `json:"found,omitempty"`
	OK      bool   `json:"ok"`
	Purpose string `json:"purpose"`
}

func (s *Server) mailRelayConfig(ctx context.Context) (domain, hostname, relayhost string) {
	v, _, _ := s.store.Setting(ctx, "mail.relay")
	var c struct{ Domain, Hostname, Relayhost string }
	if v != "" {
		_ = json.Unmarshal([]byte(v), &c)
	}
	return c.Domain, c.Hostname, c.Relayhost
}

var dkimTXTRe = regexp.MustCompile(`"([^"]*)"`)

// dkimRecord reads the generated public key from the relay container.
func (s *Server) dkimRecord(ctx context.Context, actor string) string {
	out, err := s.runner.Run(ctx, actor, "docker", "exec", relayStack+"-relay-1", "sh", "-c", "find /etc/opendkim/keys -name '*.txt' -exec cat {} +")
	if err != nil {
		return ""
	}
	var parts []string
	for _, m := range dkimTXTRe.FindAllStringSubmatch(out.Stdout, -1) {
		parts = append(parts, m[1])
	}
	return strings.Join(parts, "")
}

func lookupTXT(ctx context.Context, name string) []string {
	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "udp", "1.1.1.1:53")
	}}
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	txt, _ := r.LookupTXT(c, name)
	return txt
}

func (s *Server) mailRelayState(ctx context.Context, actor string) MailRelay {
	domain, hostname, relayhost := s.mailRelayConfig(ctx)
	m := MailRelay{Domain: domain, Hostname: hostname, Relayhost: relayhost, PublicIP: proxy.PublicIP(ctx), PanelSMTP: "127.0.0.1:2525", AppSMTP: relayStack + "-relay-1:587"}
	if domain == "" {
		return m
	}
	if info, err := s.docker.Inspect(ctx, actor, relayStack+"-relay-1"); err == nil {
		m.Installed = true
		m.Running = info.State == "running"
	}
	spf := "v=spf1 ip4:" + m.PublicIP + " -all"
	if m.PublicIP == "" {
		spf = "v=spf1 a:" + hostname + " -all"
	}
	recs := []DNSRecord{
		{Name: hostname, Type: "A", Value: m.PublicIP, Purpose: "The relay's own name; the reverse (PTR) record at your provider must point back to it."},
		{Name: domain, Type: "TXT", Value: spf, Purpose: "SPF: which servers may send for the domain."},
	}
	if dkim := s.dkimRecord(ctx, actor); dkim != "" {
		recs = append(recs, DNSRecord{Name: "mail._domainkey." + domain, Type: "TXT", Value: dkim, Purpose: "DKIM: the public key that verifies the signature on each mail."})
	} else if m.Installed {
		recs = append(recs, DNSRecord{Name: "mail._domainkey." + domain, Type: "TXT", Value: "(key still generating; reload in a few seconds)", Purpose: "DKIM"})
	}
	recs = append(recs, DNSRecord{Name: "_dmarc." + domain, Type: "TXT", Value: "v=DMARC1; p=quarantine; rua=mailto:postmaster@" + domain, Purpose: "DMARC: what receivers do with mail that fails SPF and DKIM, and where to send reports."})
	for i := range recs {
		rec := &recs[i]
		switch rec.Type {
		case "A":
			if d := proxy.CheckDNS(ctx, rec.Name); len(d.Resolved) > 0 {
				rec.Found = strings.Join(d.Resolved, ", ")
				rec.OK = d.OK
			}
		case "TXT":
			want := strings.ReplaceAll(rec.Value, " ", "")
			for _, t := range lookupTXT(ctx, rec.Name) {
				got := strings.ReplaceAll(t, " ", "")
				prefix := strings.SplitN(rec.Value, ";", 2)[0]
				if strings.HasPrefix(t, prefix) {
					rec.Found = t
					rec.OK = got == want || (strings.HasPrefix(rec.Name, "mail._domainkey.") && strings.Contains(got, "p="+strings.SplitN(strings.SplitN(want, "p=", 2)[len(strings.SplitN(want, "p=", 2))-1], ";", 2)[0]))
					if strings.HasPrefix(rec.Name, "_dmarc.") && strings.HasPrefix(got, "v=DMARC1") {
						rec.OK = true
					}
				}
			}
		}
	}
	m.Records = recs
	return m
}

// handleMailRelay shows the relay and its DNS state (GET), sets it up
// (POST) or removes it (DELETE).
func (s *Server) handleMailRelay(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	u := userFrom(r.Context())
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.mailRelayState(r.Context(), u.Username))
		return
	case http.MethodDelete:
		_ = s.docker.RemoveStack(r.Context(), u.Username, relayStack, false)
		_ = s.store.SetSetting(r.Context(), "mail.relay", "")
		_ = s.store.Audit(r.Context(), u.Username, "mail.relay", "", "removed")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var req struct{ Domain, Hostname, Relayhost, RelayUser, RelayPassword string }
	if err := decode(r, &req); err != nil {
		s.badJSON(w, err)
		return
	}
	req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))
	req.Hostname = strings.ToLower(strings.TrimSpace(req.Hostname))
	if req.Domain == "" || strings.ContainsAny(req.Domain, " /:@") {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "the sender domain is required, e.g. example.com"})
		return
	}
	if req.Hostname == "" {
		req.Hostname = "mail." + req.Domain
	}
	fields := map[string]string{"ALLOWED_SENDER_DOMAINS": req.Domain, "HOSTNAME": req.Hostname, "RELAYHOST": req.Relayhost, "RELAYHOST_USERNAME": req.RelayUser, "RELAYHOST_PASSWORD": req.RelayPassword}
	// Reinstalling with the same name rewrites the stack; the DKIM volume survives.
	rc, wait, err := s.catalog.Install(r.Context(), u.Username, catalog.InstallRequest{Slug: "postfix-relay", Name: relayStack, Fields: fields})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "install", Message: err.Error()})
		return
	}
	_, _ = io.Copy(io.Discard, rc)
	if err := wait(); err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "install", Message: "relay did not start: " + err.Error()})
		return
	}
	_ = s.proxy.Connect(r.Context(), u.Username, relayStack+"-relay-1")
	b, _ := json.Marshal(map[string]string{"domain": req.Domain, "hostname": req.Hostname, "relayhost": req.Relayhost})
	_ = s.store.SetSetting(r.Context(), "mail.relay", string(b))
	_ = s.store.Audit(r.Context(), u.Username, "mail.relay", req.Domain, req.Hostname)
	// The DKIM key is generated on first start; give it a moment.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if s.dkimRecord(r.Context(), u.Username) != "" {
			break
		}
		time.Sleep(2 * time.Second)
	}
	writeJSON(w, http.StatusOK, s.mailRelayState(r.Context(), u.Username))
}

// handleMailRelayTest sends a message through the relay with sendmail.
func (s *Server) handleMailRelayTest(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	u := userFrom(r.Context())
	var req struct{ To string }
	if err := decode(r, &req); err != nil || !strings.Contains(req.To, "@") {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "a recipient address is required"})
		return
	}
	domain, hostname, _ := s.mailRelayConfig(r.Context())
	if domain == "" {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "set up the relay first"})
		return
	}
	msg := "From: Islet <noreply@" + domain + ">\nTo: " + req.To + "\nSubject: Islet mail relay test\n\nThis message left " + hostname + " through the Islet relay at " + time.Now().UTC().Format(time.RFC1123) + ".\nIf it landed in spam, check the SPF, DKIM and DMARC records on the Notifications page and the PTR record at your provider.\n"
	out, err := s.runner.RunInput(r.Context(), u.Username, []byte(msg), "docker", "exec", "-i", relayStack+"-relay-1", "sendmail", "-f", "noreply@"+domain, req.To)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "send", Message: strings.TrimSpace(out.Stderr + " " + err.Error())})
		return
	}
	queue, _ := s.runner.Run(r.Context(), u.Username, "docker", "exec", relayStack+"-relay-1", "sh", "-c", "sleep 2; mailq | tail -n 3")
	writeJSON(w, http.StatusOK, map[string]string{"status": "queued", "queue": strings.TrimSpace(queue.Stdout)})
}
