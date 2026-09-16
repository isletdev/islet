package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/isletdev/islet/internal/proxy"
	"github.com/isletdev/islet/pkg/api"
)

func (s *Server) handleMaintenancePage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Retry-After", "120")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(proxy.MaintenancePage))
}

// handleBlockedPage answers the requests the exploit filter caught.
//
// Plain text and short: nothing that reaches this is a browser somebody is
// looking at, and a 403 that explains itself to a scanner only tells it which
// filter it hit.
func (s *Server) handleBlockedPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte("Forbidden\n"))
}

func (s *Server) handleProxyStatus(w http.ResponseWriter, r *http.Request) {
	// Diagnose reads Traefik's log, so it is not part of Status, which is
	// called from inside the install path itself.
	st := s.proxy.Status(r.Context())
	st.Problems = s.proxy.Diagnose(r.Context(), st)
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleProxyInstall(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins can install the proxy"})
		return
	}
	var req struct {
		AcmeEmail   string            `json:"acmeEmail"`
		DNSProvider *string           `json:"dnsProvider"`
		DNSEnv      map[string]string `json:"dnsEnv"`
	}
	// Decode whatever body there is, without asking how long it is first.
	//
	// ContentLength is -1 when the length is unknown, which is what a chunked
	// request looks like — and a request that reached the panel through a
	// reverse proxy, including Islet's own Traefik once the panel has a
	// domain, is routinely re-encoded that way. Gating on "> 0" therefore
	// skipped the body entirely for exactly those users: the email, the DNS
	// provider and its credentials all arrived empty, the proxy was rebuilt
	// with no certificate resolver, and the call still answered 200.
	if err := decode(r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	if req.DNSProvider != nil {
		if err := s.proxy.SetDNS(r.Context(), strings.TrimSpace(*req.DNSProvider), req.DNSEnv); err != nil {
			writeJSON(w, http.StatusBadRequest, api.Error{Error: "dns", Message: err.Error()})
			return
		}
	}
	if err := s.proxy.Install(r.Context(), u.Username, strings.TrimSpace(req.AcmeEmail)); err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "proxy", Message: err.Error()})
		return
	}
	if err := s.proxy.Reconcile(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.proxy.Status(r.Context()))
}

// handleImportScan reads every place a known reverse proxy keeps its sites and
// reports what is there. It only reads: whatever is serving these names keeps
// serving them until somebody moves it aside.
func (s *Server) handleImportScan(w http.ResponseWriter, r *http.Request) {
	if userFrom(r.Context()).Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins import sites"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"found": proxy.Discover()})
}

// handleImport previews or imports virtual hosts as domains.
//
// Pasted text says which product wrote it by its own shape, so the request does
// not carry a format: somebody pasting a Caddyfile knows it is one and should
// not have to say so.
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins import sites"})
		return
	}
	var req struct {
		Text string `json:"text"`
		Save bool   `json:"save"`
		// Only these hosts are written. Empty means every one that can be,
		// which is what the old endpoint did.
		Hosts []string `json:"hosts"`
	}
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	var sites []proxy.Site
	if req.Text != "" {
		sites, _ = proxy.ParseText(req.Text)
	} else {
		for _, f := range proxy.Discover() {
			sites = append(sites, f.Sites...)
		}
	}
	wanted := map[string]bool{}
	for _, h := range req.Hosts {
		wanted[strings.ToLower(strings.TrimSpace(h))] = true
	}
	type proposalLocation struct {
		Path      string `json:"path"`
		Target    string `json:"target"`
		StripPath bool   `json:"stripPath"`
		Note      string `json:"note,omitempty"`
	}
	type proposal struct {
		Host      string             `json:"host"`
		Target    string             `json:"target"`
		Note      string             `json:"note"`
		Source    string             `json:"source,omitempty"`
		File      string             `json:"file,omitempty"`
		Locations []proposalLocation `json:"locations,omitempty"`
		// Skipped names the parts of this host that were found and could not
		// be translated. Saying so is the point: an import that drops a path
		// in silence looks complete and is not.
		Skipped []string `json:"skipped,omitempty"`
		Saved   bool     `json:"saved"`
	}

	// resolve turns an upstream into the best target Islet can express. A
	// container by that name becomes a container target, so Islet attaches it
	// to the proxy network itself rather than relying on the two proxies
	// happening to share one.
	resolve := func(upstream string) (kind, target string, port int, label string, container bool) {
		if name, p, ok := proxy.UpstreamParts(upstream); ok {
			if _, err := s.docker.Inspect(r.Context(), u.Username, name); err == nil {
				return "container", name, p, name + ":" + strconv.Itoa(p), true
			}
		}
		return "url", upstream, 0, upstream, false
	}
	out := []proposal{}
	for _, site := range sites {
		for _, h := range site.Hosts {
			p := proposal{Host: h, Source: site.Source, File: site.File}
			// A container upstream becomes a container target, so Islet
			// attaches it to the proxy network itself instead of relying on
			// the two proxies sharing one.
			dom := proxy.Domain{
				Host: h, TLS: "letsencrypt", Enabled: true,
				// Carried across rather than reset to a default: these are
				// decisions the person already made, in the configuration
				// being replaced.
				PassHost:      site.PassHost,
				BlockExploits: site.BlockExploits,
			}
			p.Skipped = site.Skipped

			// Locations first: one of them may have to stand in as the root.
			locs := site.Locations
			root := site.Upstream
			rootNote := ""
			if root == "" && site.NoRoot && len(locs) > 0 {
				// Nothing served the root there either. The shortest path is
				// the closest thing to a root, and taking it keeps the host
				// answering instead of returning 404 at the top.
				pick := 0
				for i := range locs {
					if len(locs[i].Path) < len(locs[pick].Path) {
						pick = i
					}
				}
				root = locs[pick].Upstream
				rootNote = "nothing served / there, so " + locs[pick].Path + " stands in as the root; change it after importing if that is wrong"
				locs = append(append([]proxy.SiteLocation{}, locs[:pick]...), locs[pick+1:]...)
			}
			for _, l := range locs {
				pl := proposalLocation{Path: l.Path, StripPath: l.StripPath}
				if l.Upstream == "" {
					pl.Note = "the upstream is " + l.RawUp + " and the lines that define it are not in this text"
					p.Locations = append(p.Locations, pl)
					continue
				}
				kind, target, port, label, _ := resolve(l.Upstream)
				pl.Target = label
				p.Locations = append(p.Locations, pl)
				dom.Locations = append(dom.Locations, proxy.Location{
					Path: l.Path, TargetType: kind, Target: target, Port: port, StripPath: l.StripPath,
				})
			}

			switch {
			case root != "":
				kind, target, port, label, container := resolve(root)
				dom.TargetType, dom.Target, dom.Port = kind, target, port
				p.Target = label
				if container {
					p.Note = "forwards to the container " + target + "; Islet attaches it to the proxy network when you import"
				} else {
					p.Note = "proxied to the same place " + sourceName(site.Source) + " sends it; the app keeps running and Traefik takes over the domain"
				}
				if rootNote != "" {
					p.Note = rootNote
				}
				if n := len(dom.Locations); n > 0 {
					p.Note += fmt.Sprintf(". %d custom location(s) come across with it", n)
				}
				var carried []string
				if site.BlockExploits {
					carried = append(carried, "blocking common exploits")
				}
				if !site.PassHost {
					carried = append(carried, "sending the upstream's own hostname")
				}
				if site.WebSockets {
					// Not a setting on this side: Traefik proxies a WebSocket
					// whatever anyone ticks. Saying so beats leaving somebody
					// to wonder which of their options survived.
					carried = append(carried, "WebSockets, which need no setting here")
				}
				if len(carried) > 0 {
					p.Note += ". Kept: " + strings.Join(carried, ", ")
				}
			case site.RawUp != "":
				p.Note = "the upstream is " + site.RawUp + " and the variables behind it are not in this text; paste the whole file, including the lines that define them"
			case site.Root != "":
				p.Note = "static site under " + site.Root + ": deploy it as an app (New app, local path) or serve it from a container; not imported"
				if len(p.Locations) > 0 {
					p.Note += ". Its " + strconv.Itoa(len(p.Locations)) + " forwarded path(s) cannot come across on their own, because nothing would answer the root"
				}
			default:
				p.Note = "no proxy_pass or root; not imported"
			}
			if req.Save && p.Target != "" && (len(wanted) == 0 || wanted[strings.ToLower(h)]) {
				if _, err := s.proxy.Save(r.Context(), u.Username, &dom); err == nil {
					p.Saved = true
				} else {
					p.Note = err.Error()
				}
			}
			out = append(out, p)
		}
	}
	if req.Save {
		saved := 0
		for _, p := range out {
			if p.Saved {
				saved++
			}
		}
		_ = s.store.Audit(r.Context(), u.Username, "domain.import", "sites", strconv.Itoa(saved))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleProxyRemove(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins can remove the proxy"})
		return
	}
	if err := s.proxy.Remove(r.Context(), u.Username); err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "proxy", Message: err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleProxyCerts(w http.ResponseWriter, r *http.Request) {
	certs, err := s.proxy.Certificates()
	if err != nil {
		writeJSON(w, http.StatusOK, []proxy.Cert{})
		return
	}
	writeJSON(w, http.StatusOK, certs)
}

func (s *Server) handlePreviewHost(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("name")))
	if name == "" {
		name = "app"
	}
	writeJSON(w, http.StatusOK, map[string]string{"host": proxy.PreviewHost(r.Context(), name), "publicIp": proxy.PublicIP(r.Context())})
}

func (s *Server) handleDomains(w http.ResponseWriter, r *http.Request) {
	list, err := s.proxy.Domains(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	if u := userFrom(r.Context()); scoped(u) {
		kept := list[:0]
		for _, d := range list {
			if d.TargetType == "container" && s.allowsContainer(r.Context(), u, d.Target) {
				kept = append(kept, d)
			}
		}
		list = kept
	}
	if userFrom(r.Context()).Role != "admin" {
		for i := range list {
			list[i].BasicAuth = ""
		}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleDomainSave(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role == "viewer" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "viewers cannot change domains"})
		return
	}
	// An absent field takes the panel's own default, the way the cron, uptime
	// and backup handlers already work. Without it a client that sends only
	// what it cares about — an agent through MCP, a script — creates a domain
	// with enabled false: stored, listed, never served and never issued a
	// certificate, with nothing to say why.
	d := proxy.Domain{Enabled: true, PassHost: true}
	if err := decode(r, &d); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	var before *proxy.Domain
	if id := r.PathValue("id"); id != "" {
		d.ID = id
		cur, err := s.proxy.Domain(r.Context(), id)
		if err != nil {
			writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: "no such domain"})
			return
		}
		before = cur
	} else {
		d.ID = ""
	}
	if d.TargetType == "panel" && u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins can route the panel"})
		return
	}
	// Who may reach a site is an access-control decision, not a routing one.
	// The panel has only ever offered this form to admins; the API had not
	// caught up, so a deployer could turn a gate off, or add themselves to its
	// list, through a plain PUT — or through the assistant, which speaks the
	// same API.
	if u.Role != "admin" && !d.ProtectionEquals(before) {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins can change who may reach a site"})
		return
	}
	saved, err := s.proxy.Save(r.Context(), u.Username, &d)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleDomainDelete(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role == "viewer" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "viewers cannot change domains"})
		return
	}
	if err := s.proxy.Delete(r.Context(), u.Username, r.PathValue("id")); err != nil {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: "no such domain"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDomainDNS(w http.ResponseWriter, r *http.Request) {
	d, err := s.proxy.Domain(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: "no such domain"})
		return
	}
	writeJSON(w, http.StatusOK, proxy.CheckDNS(r.Context(), d.Host))
}

func (s *Server) handleDNSCheck(w http.ResponseWriter, r *http.Request) {
	host := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("host")))
	if host == "" {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "host is required"})
		return
	}
	writeJSON(w, http.StatusOK, proxy.CheckDNS(r.Context(), host))
}

// sourceName names the software a site came out of, for a note that reads the
// same whether the file was nginx's, Caddy's or Apache's.
func sourceName(source string) string {
	if source == "" {
		return "the old proxy"
	}
	return source
}
