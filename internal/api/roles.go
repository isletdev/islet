package api

import (
	"net/http"

	"github.com/isletdev/islet/pkg/api"
)

// The least role that may reach each route.
//
// Authorization used to live in the handlers: ninety-six hand-written role
// checks spread over twenty-five files, with nothing above them. Nothing in
// the middleware chain looked at a role at all, so a route registered without
// its check was reachable by anybody signed in, silently, and stayed that way
// until somebody read that particular handler. Three holes were found exactly
// that way — writing and deleting vault secrets, emitting notifications, and
// removing the protection from a domain — and what they had in common was not
// a wrong decision but a missing one.
//
// So the decision moves here, where it can be read in one sitting and reviewed
// as a whole. Every authenticated route needs a line; TestEveryRouteDeclaresARole
// fails when one is added without one, and the lookup below refuses anything it
// does not recognise rather than waving it through. Forgetting is now a red
// test instead of an open endpoint.
//
// The checks inside the handlers stay. They say the same thing in the place a
// reader of that handler will look, several of them phrase the refusal better
// than a generic one could, and a few decide on more than the route — whether a
// domain is protected, whether the path is under /etc. This table is the floor,
// not a replacement for judgement below it.
var minRole = map[string]string{
	"GET /api/v1/auth/cookie-domain":                                viewer,
	"POST /api/v1/auth/cookie-domain":                               admin,
	"GET /api/v1/auth/me":                                           viewer,
	"POST /api/v1/auth/password":                                    viewer,
	"POST /api/v1/auth/totp/setup":                                  viewer,
	"POST /api/v1/auth/totp/enable":                                 viewer,
	"POST /api/v1/auth/totp/disable":                                viewer,
	"GET /api/v1/auth/sessions":                                     viewer,
	"GET /api/v1/auth/tokens":                                       viewer,
	"GET /api/v1/users":                                             admin,
	"POST /api/v1/users":                                            admin,
	"PUT /api/v1/users/{id}":                                        admin,
	"DELETE /api/v1/users/{id}":                                     admin,
	"GET /api/v1/attention":                                         viewer,
	"GET /api/v1/github":                                            viewer,
	"POST /api/v1/github":                                           admin,
	"GET /api/v1/github/repos":                                      viewer,
	"GET /api/v1/vault":                                             viewer,
	"POST /api/v1/vault":                                            admin,
	"PUT /api/v1/vault/{name}":                                      admin,
	"DELETE /api/v1/vault/{name}":                                   admin,
	"POST /api/v1/vault/{name}/reveal":                              admin,
	"GET /api/v1/ai/providers":                                      viewer,
	"POST /api/v1/ai/providers":                                     admin,
	"POST /api/v1/ai/providers/{id}":                                admin,
	"PUT /api/v1/ai/providers/{id}":                                 admin,
	"DELETE /api/v1/ai/providers/{id}":                              admin,
	"GET /api/v1/assistant":                                         viewer,
	"POST /api/v1/assistant":                                        admin,
	"POST /api/v1/assistant/chat":                                   viewer,
	"GET /api/v1/assistant/chats":                                   viewer,
	"POST /api/v1/assistant/chats":                                  viewer,
	"GET /api/v1/assistant/chats/{id}":                              viewer,
	"POST /api/v1/assistant/chats/{id}":                             viewer,
	"DELETE /api/v1/assistant/chats/{id}":                           viewer,
	"GET /api/v1/assistant/uploads":                                 viewer,
	"POST /api/v1/assistant/uploads":                                deployer,
	"GET /api/v1/assistant/uploads/{id}":                            viewer,
	"DELETE /api/v1/assistant/uploads/{id}":                         deployer,
	"GET /api/v1/assistant/runs":                                    viewer,
	"GET /api/v1/assistant/runs/{id}":                               viewer,
	"POST /api/v1/assistant/runs/{id}/cancel":                       viewer,
	"GET /api/v1/mcp":                                               viewer,
	"GET /api/v1/report/weekly":                                     viewer,
	"POST /api/v1/report/weekly":                                    admin,
	"POST /api/v1/report/weekly/send":                               admin,
	"POST /api/v1/mcp":                                              admin,
	"GET /api/v1/diagnostics":                                       viewer,
	"POST /api/v1/auth/tokens":                                      viewer,
	"DELETE /api/v1/auth/tokens/{id}":                               viewer,
	"DELETE /api/v1/auth/sessions/{id}":                             viewer,
	"GET /api/v1/system":                                            viewer,
	"GET /api/v1/system/processes":                                  viewer,
	"GET /api/v1/system/ports":                                      viewer,
	"GET /api/v1/metrics/latest":                                    viewer,
	"GET /api/v1/metrics/history":                                   viewer,
	"GET /api/v1/metrics/live":                                      viewer,
	"GET /api/v1/terminal/ws":                                       admin,
	"GET /api/v1/workspaces":                                        admin,
	"POST /api/v1/workspaces":                                       admin,
	"GET /api/v1/workspaces/{id}":                                   admin,
	"PUT /api/v1/workspaces/{id}":                                   admin,
	"DELETE /api/v1/workspaces/{id}":                                admin,
	"POST /api/v1/workspaces/{id}/start":                            admin,
	"POST /api/v1/workspaces/{id}/stop":                             admin,
	"GET /api/v1/workspaces/{id}/history":                           admin,
	"GET /api/v1/workspaces/{id}/mcp":                               admin,
	"GET /api/v1/workspaces/{id}/agents":                            admin,
	"POST /api/v1/workspaces/{id}/agents":                           admin,
	"GET /api/v1/workspaces/{id}/agents/{agentId}":                  admin,
	"PUT /api/v1/workspaces/{id}/agents/{agentId}":                  admin,
	"DELETE /api/v1/workspaces/{id}/agents/{agentId}":               admin,
	"POST /api/v1/workspaces/{id}/agents/{agentId}/start":           admin,
	"POST /api/v1/workspaces/{id}/agents/{agentId}/stop":            admin,
	"GET /api/v1/workspaces/{id}/agents/{agentId}/history":          admin,
	"GET /api/v1/workspaces/{id}/agents/{agentId}/attach":           admin,
	"POST /api/v1/workspaces/{id}/mcp":                              admin,
	"POST /api/v1/workspaces/tmux":                                  admin,
	"POST /api/v1/workspaces/claude":                                admin,
	"GET /api/v1/workspaces/{id}/attach":                            admin,
	"GET /api/v1/audit":                                             viewer,
	"GET /api/v1/servers":                                           admin,
	"GET /api/v1/servers/key":                                       admin,
	"POST /api/v1/servers":                                          admin,
	"POST /api/v1/servers/{id}/join":                                admin,
	"GET /api/v1/servers/{id}/join/events":                          admin,
	"POST /api/v1/servers/{id}/check":                               admin,
	"GET /api/v1/servers/{id}/exposure":                             admin,
	"DELETE /api/v1/servers/{id}":                                   admin,
	"GET /api/v1/commands":                                          admin,
	"GET /api/v1/files":                                             viewer,
	"GET /api/v1/files/read":                                        viewer,
	"GET /api/v1/files/tail":                                        viewer,
	"PUT /api/v1/files/write":                                       admin,
	"POST /api/v1/files/op":                                         admin,
	"GET /api/v1/files/trash":                                       admin,
	"POST /api/v1/files/trash":                                      admin,
	"GET /api/v1/files/download":                                    viewer,
	"POST /api/v1/files/upload":                                     admin,
	"GET /api/v1/files/search":                                      viewer,
	"GET /api/v1/files/usage":                                       viewer,
	"GET /api/v1/files/checksum":                                    viewer,
	"GET /api/v1/proxy":                                             viewer,
	"POST /api/v1/proxy/install":                                    admin,
	"POST /api/v1/proxy/remove":                                     admin,
	"GET /api/v1/proxy/dns-providers":                               viewer,
	"GET /api/v1/domains/import/scan":                               admin,
	"POST /api/v1/domains/import":                                   admin,
	"GET /api/v1/proxy/certs":                                       viewer,
	"GET /api/v1/proxy/preview-host":                                viewer,
	"GET /api/v1/domains":                                           viewer,
	"POST /api/v1/domains":                                          deployer,
	"PUT /api/v1/domains/{id}":                                      deployer,
	"DELETE /api/v1/domains/{id}":                                   deployer,
	"GET /api/v1/domains/{id}/dns":                                  viewer,
	"GET /api/v1/dns-check":                                         viewer,
	"GET /api/v1/catalog":                                           viewer,
	"GET /api/v1/recipes":                                           viewer,
	"GET /api/v1/catalog/source":                                    admin,
	"POST /api/v1/catalog/source":                                   admin,
	"DELETE /api/v1/catalog/source":                                 admin,
	"GET /api/v1/mail/relay":                                        admin,
	"POST /api/v1/mail/relay":                                       admin,
	"DELETE /api/v1/mail/relay":                                     admin,
	"POST /api/v1/mail/relay/test":                                  admin,
	"POST /api/v1/recipes/{slug}/run":                               admin,
	"GET /api/v1/catalog/installed":                                 viewer,
	"GET /api/v1/catalog/installed/updates":                         viewer,
	"POST /api/v1/catalog/installed/{name}/update":                  deployer,
	"GET /api/v1/catalog/{slug}":                                    viewer,
	"POST /api/v1/catalog/{slug}/install":                           deployer,
	"GET /api/v1/apps":                                              viewer,
	"POST /api/v1/apps":                                             admin,
	"GET /api/v1/apps/env-groups":                                   viewer,
	"POST /api/v1/apps/env-groups":                                  admin,
	"DELETE /api/v1/apps/env-groups/{name}":                         admin,
	"POST /api/v1/apps/inspect":                                     admin,
	"GET /api/v1/apps/{id}":                                         viewer,
	"PUT /api/v1/apps/{id}":                                         admin,
	"DELETE /api/v1/apps/{id}":                                      admin,
	"POST /api/v1/apps/{id}/deploy":                                 deployer,
	"GET /api/v1/apps/{id}/deploy/log":                              viewer,
	"POST /api/v1/apps/{id}/services":                               admin,
	"POST /api/v1/apps/{id}/cancel":                                 deployer,
	"POST /api/v1/apps/{id}/promote":                                deployer,
	"POST /api/v1/apps/{id}/upload":                                 admin,
	"GET /api/v1/sidebar":                                           viewer,
	"POST /api/v1/sidebar":                                          admin,
	"GET /api/v1/auth/geo":                                          viewer,
	"POST /api/v1/auth/geo":                                         admin,
	"POST /api/v1/databases/{name}/adminer":                         admin,
	"GET /api/v1/apps/{id}/releases":                                viewer,
	"GET /api/v1/apps/{id}/releases/{release}":                      viewer,
	"GET /api/v1/backups":                                           viewer,
	"GET /api/v1/backups/kit":                                       admin,
	"POST /api/v1/backups/destinations":                             admin,
	"PUT /api/v1/backups/destinations/{id}":                         admin,
	"DELETE /api/v1/backups/destinations/{id}":                      admin,
	"POST /api/v1/backups/destinations/{id}/restore-test":           admin,
	"POST /api/v1/backups/destinations/{id}/verify":                 admin,
	"GET /api/v1/backups/destinations/{id}/snapshots":               viewer,
	"GET /api/v1/backups/destinations/{id}/snapshots/{snapshot}/ls": viewer,
	"POST /api/v1/backups/destinations/{id}/restore":                admin,
	"POST /api/v1/backups/destinations/{id}/restore-database":       admin,
	"GET /api/v1/backups/host":                                      admin,
	"POST /api/v1/backups/host":                                     admin,
	"DELETE /api/v1/backups/host":                                   admin,
	"POST /api/v1/backups/plans":                                    admin,
	"PUT /api/v1/backups/plans/{id}":                                admin,
	"DELETE /api/v1/backups/plans/{id}":                             admin,
	"POST /api/v1/backups/plans/{id}/run":                           deployer,
	"POST /api/v1/backups/plans/{id}/cancel":                        admin,
	"GET /api/v1/backups/plans/{id}/runs":                           viewer,
	"GET /api/v1/backups/plans/{id}/runs/{run}":                     viewer,
	"GET /api/v1/security":                                          viewer,
	"POST /api/v1/security/fix-all":                                 admin,
	"POST /api/v1/security/fix/{id}":                                admin,
	"POST /api/v1/security/firewall/rules":                          admin,
	"DELETE /api/v1/security/firewall/rules":                        admin,
	"POST /api/v1/security/unban":                                   admin,
	"GET /api/v1/security/blocklist":                                admin,
	"POST /api/v1/security/blocklist":                               admin,
	"POST /api/v1/security/blocklist/refresh":                       admin,
	"DELETE /api/v1/security/blocklist":                             admin,
	"POST /api/v1/security/ssh":                                     admin,
	"POST /api/v1/security/ssh/confirm":                             admin,
	"POST /api/v1/security/scan":                                    admin,
	"POST /api/v1/security/firewall/panel":                          admin,
	"POST /api/v1/security/lynis":                                   admin,
	"POST /api/v1/security/panic":                                   admin,
	"POST /api/v1/security/host/user":                               admin,
	"POST /api/v1/security/host/sshkey":                             admin,
	"GET /api/v1/security/host/timezone":                            viewer,
	"POST /api/v1/security/host/timezone":                           admin,
	"GET /api/v1/security/host/audit":                               viewer,
	"POST /api/v1/security/host/audit":                              admin,
	"POST /api/v1/security/host/baseline":                           admin,
	"GET /api/v1/runners":                                           viewer,
	"POST /api/v1/runners":                                          admin,
	"PUT /api/v1/runners/{id}":                                      admin,
	"DELETE /api/v1/runners/{id}":                                   admin,
	"GET /api/v1/runners/{id}/jobs":                                 viewer,
	"GET /api/v1/runners/{id}/workflow":                             viewer,
	"GET /api/v1/uptime/checks":                                     viewer,
	"POST /api/v1/uptime/checks":                                    deployer,
	"PUT /api/v1/uptime/checks/{id}":                                deployer,
	"DELETE /api/v1/uptime/checks/{id}":                             deployer,
	"GET /api/v1/uptime/checks/{id}/results":                        viewer,
	"POST /api/v1/uptime/checks/{id}/probe":                         deployer,
	"GET /api/v1/logs/sources":                                      viewer,
	"GET /api/v1/logs/stream":                                       admin,
	"GET /api/v1/databases":                                         viewer,
	"GET /api/v1/databases/{name}":                                  viewer,
	"POST /api/v1/databases/{name}/databases":                       admin,
	"DELETE /api/v1/databases/{name}/databases/{db}":                admin,
	"GET /api/v1/databases/{name}/slow":                             viewer,
	"POST /api/v1/databases/{name}/extensions":                      admin,
	"POST /api/v1/databases/{name}/dumps":                           admin,
	"POST /api/v1/databases/{name}/restore":                         admin,
	"GET /api/v1/databases/{name}/dumps/{file}":                     admin,
	"DELETE /api/v1/databases/{name}/dumps/{file}":                  admin,
	"POST /api/v1/databases/{name}/schedule":                        admin,
	"POST /api/v1/databases/{name}/public":                          admin,
	"POST /api/v1/databases/{name}/pooler":                          admin,
	"GET /api/v1/cron/jobs":                                         viewer,
	"POST /api/v1/cron/jobs":                                        admin,
	"GET /api/v1/cron/jobs/{id}":                                    viewer,
	"PUT /api/v1/cron/jobs/{id}":                                    admin,
	"DELETE /api/v1/cron/jobs/{id}":                                 admin,
	"POST /api/v1/cron/jobs/{id}/run":                               deployer,
	"POST /api/v1/cron/jobs/{id}/kill":                              deployer,
	"GET /api/v1/cron/jobs/{id}/runs":                               viewer,
	"GET /api/v1/cron/jobs/{id}/runs/{run}":                         viewer,
	"GET /api/v1/cron/jobs/{id}/versions":                           viewer,
	"GET /api/v1/cron/jobs/{id}/versions/{version}":                 deployer,
	"GET /api/v1/cron/jobs/{id}/export":                             deployer,
	"POST /api/v1/cron/preview":                                     viewer,
	"GET /api/v1/cron/templates":                                    viewer,
	"POST /api/v1/cron/lint":                                        viewer,
	"POST /api/v1/cron/import":                                      admin,
	"GET /api/v1/notify/channels":                                   viewer,
	"POST /api/v1/notify/channels":                                  admin,
	"PUT /api/v1/notify/channels/{id}":                              admin,
	"DELETE /api/v1/notify/channels/{id}":                           admin,
	"POST /api/v1/notify/channels/{id}/test":                        admin,
	"POST /api/v1/notify/telegram/detect":                           admin,
	"GET /api/v1/notify/events":                                     viewer,
	"GET /api/v1/notify/events/{id}/deliveries":                     viewer,
	"POST /api/v1/notify/emit":                                      deployer,
	"GET /api/v1/docker/status":                                     viewer,
	"GET /api/v1/docker/containers":                                 viewer,
	"GET /api/v1/docker/containers/{id}":                            viewer,
	"GET /api/v1/docker/containers/{id}/logs":                       viewer,
	"GET /api/v1/docker/containers/{id}/exec":                       admin,
	"POST /api/v1/docker/containers/{id}/limits":                    admin,
	"POST /api/v1/docker/containers/{id}/{action}":                  deployer,
	"GET /api/v1/docker/images":                                     viewer,
	"GET /api/v1/docker/registries":                                 viewer,
	"POST /api/v1/docker/registries":                                admin,
	"DELETE /api/v1/docker/registries/{host}":                       admin,
	"POST /api/v1/docker/images/pull":                               deployer,
	"DELETE /api/v1/docker/images/{id}":                             deployer,
	"GET /api/v1/docker/volumes":                                    viewer,
	"DELETE /api/v1/docker/volumes/{name}":                          admin,
	"GET /api/v1/docker/networks":                                   viewer,
	"DELETE /api/v1/docker/networks/{name}":                         admin,
	"GET /api/v1/docker/df":                                         viewer,
	"POST /api/v1/docker/prune":                                     admin,
	"GET /api/v1/docker/stacks":                                     viewer,
	"POST /api/v1/docker/stacks/import":                             admin,
	"POST /api/v1/docker/stacks":                                    admin,
	"GET /api/v1/docker/stacks/{name}":                              viewer,
	"PUT /api/v1/docker/stacks/{name}":                              admin,
	"DELETE /api/v1/docker/stacks/{name}":                           admin,
	"POST /api/v1/docker/stacks/{name}/{action}":                    admin,
	"GET /api/v1/system/disk":                                       viewer,
	"POST /api/v1/system/disk/clean":                                admin,
	"GET /api/v1/system/update":                                     viewer,
	"POST /api/v1/system/update":                                    admin,
}

// Roles, weakest first. A role covers everything the ones before it cover.
const (
	viewer   = "viewer"
	deployer = "deployer"
	admin    = "admin"
)

var roleRank = map[string]int{viewer: 1, deployer: 2, admin: 3}

// allows answers whether have is at least want.
//
// An unknown role ranks zero and is allowed nothing, so a row that arrives with
// a role this build does not know — a newer daemon's database opened by an
// older one, say — loses access rather than gaining it.
func allows(have, want string) bool { return roleRank[have] >= roleRank[want] }

// enforceRole is the floor every authenticated route passes through.
//
// It keys on the routing pattern rather than the path, so a template stays one
// decision no matter how many ids it is called with, and so the table reads
// like the route table it mirrors. A pattern with no entry is refused: the cost
// of that is a 403 on a route somebody forgot to classify, which a test catches
// before it ships, and the cost of the other default is an open endpoint nobody
// finds.
func (s *Server) enforceRole(w http.ResponseWriter, r *http.Request) bool {
	want, ok := minRole[r.Pattern]
	if !ok {
		want = admin
	}
	if allows(userFrom(r.Context()).Role, want) {
		return true
	}
	writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: refusal(want)})
	return false
}

// refusal says which role a route needs, because "forbidden" on its own leaves
// the reader guessing whether they are signed in as the wrong person or the
// feature is broken.
func refusal(want string) string {
	switch want {
	case admin:
		return "only admins can do this"
	case deployer:
		return "this needs a deployer or admin account"
	}
	return "your account cannot do this"
}
