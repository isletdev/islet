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

// handleNginxImport previews or imports nginx server blocks as domains.
func (s *Server) handleNginxImport(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins import sites"})
		return
	}
	var req struct {
		Text string `json:"text"`
		Save bool   `json:"save"`
	}
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	var sites []proxy.NginxSite
	if req.Text != "" {
		sites = proxy.ParseNginx(req.Text, "pasted")
	} else {
		sites = proxy.ReadNginxSites()
	}
	type proposal struct {
		Host   string `json:"host"`
		Target string `json:"target"`
		Note   string `json:"note"`
		Saved  bool   `json:"saved"`
	}
	out := []proposal{}
	for _, site := range sites {
		for _, h := range site.Hosts {
			p := proposal{Host: h}
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
					p.Note = "proxied to the same upstream nginx used; the app keeps running, Traefik takes over the domain"
				}
			case site.RawUp != "":
				p.Note = "upstream is " + site.RawUp + " and the variables behind it are not in this text; paste the whole file, including the set directives above the server block"
			case site.Root != "":
				p.Note = "static site under " + site.Root + ": deploy it as an app (New app, local path) or serve it from a container; not imported"
			default:
				p.Note = "no proxy_pass or root; not imported"
			}
			if req.Save && p.Target != "" {
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
		_ = s.store.Audit(r.Context(), u.Username, "domain.import", "nginx", strconv.Itoa(len(out)))
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
