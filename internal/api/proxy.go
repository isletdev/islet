package api

import (
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

func (s *Server) handleProxyStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.proxy.Status(r.Context()))
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
	if r.ContentLength > 0 {
		if err := decode(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
			return
		}
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
	type proposal struct {
		Host   string `json:"host"`
		Target string `json:"target"`
		Note   string `json:"note"`
		Source string `json:"source,omitempty"`
		File   string `json:"file,omitempty"`
		Saved  bool   `json:"saved"`
	}
	out := []proposal{}
	for _, site := range sites {
		for _, h := range site.Hosts {
			p := proposal{Host: h, Source: site.Source, File: site.File}
			// A container upstream becomes a container target, so Islet
			// attaches it to the proxy network itself instead of relying on
			// the two proxies sharing one.
			dom := proxy.Domain{Host: h, TLS: "letsencrypt", Enabled: true}
			switch {
			case site.Upstream != "":
				p.Target = site.Upstream
				dom.TargetType, dom.Target = "url", site.Upstream
				if name, port, ok := proxy.UpstreamParts(site.Upstream); ok {
					if _, err := s.docker.Inspect(r.Context(), u.Username, name); err == nil {
						dom.TargetType, dom.Target, dom.Port = "container", name, port
						p.Target = name + ":" + strconv.Itoa(port)
						p.Note = "forwards to the container " + name + "; Islet attaches it to the proxy network when you import"
					}
				}
				if p.Note == "" {
					p.Note = "proxied to the same place " + sourceName(site.Source) + " sends it; the app keeps running and Traefik takes over the domain"
				}
			case site.RawUp != "":
				p.Note = "the upstream is " + site.RawUp + " and the variables behind it are not in this text; paste the whole file, including the lines that define them"
			case site.Root != "":
				p.Note = "static site under " + site.Root + ": deploy it as an app (New app, local path) or serve it from a container; not imported"
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
	var d proxy.Domain
	if err := decode(r, &d); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	if id := r.PathValue("id"); id != "" {
		d.ID = id
		if _, err := s.proxy.Domain(r.Context(), id); err != nil {
			writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: "no such domain"})
			return
		}
	} else {
		d.ID = ""
	}
	if d.TargetType == "panel" && u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins can route the panel"})
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
