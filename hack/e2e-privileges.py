"""Prove the privilege fixes hold against a running daemon: a viewer must not
read protected paths, open a shell, see the command drawer, or mint a token that
outranks them."""
import http.cookiejar, json, os, sys, urllib.error, urllib.request

sys.stdout.reconfigure(encoding="utf-8")
BASE = "http://127.0.0.1:9443"
ADMIN = sys.argv[1]


def client(cookie=None):
    cj = http.cookiejar.CookieJar()
    op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cj))
    if cookie:
        cj.set_cookie(http.cookiejar.Cookie(0, "islet_session", cookie, None, False,
                                            "127.0.0.1", False, False, "/", True, False,
                                            None, False, None, None, {}))
    return op, cj


def call(op, method, path, body=None, headers=None):
    data = None if body is None else json.dumps(body).encode()
    h = {"Content-Type": "application/json", "Sec-Fetch-Site": "same-origin"}
    h.update(headers or {})
    req = urllib.request.Request(BASE + path, data=data, method=method, headers=h)
    try:
        with op.open(req, timeout=60) as r:
            return r.status, r.read().decode(errors="replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode(errors="replace")


admin, _ = client(ADMIN)
fails = []


def check(name, got, want):
    ok = got == want
    print(("PASS " if ok else "FAIL ") + name + f"  (got {got}, want {want})")
    if not ok:
        fails.append(name)


# A viewer to attack with.
st, body = call(admin, "POST", "/api/v1/users", {"username": "spy", "password": "a strong password 123", "role": "viewer"})
print("create viewer ->", st)

viewer, cj = client()
st, body = call(viewer, "POST", "/api/v1/auth/login", {"username": "spy", "password": "a strong password 123"})
print("viewer login ->", st)
if st != 200:
    print("cannot continue without a viewer session:", body[:200])
    sys.exit(1)

# 1. The doubled-slash bypass of the protected-path guard.
st, _ = call(viewer, "GET", "/api/v1/files/read?path=/var/lib/islet//secret.key")
check("viewer cannot read the master key with a doubled slash", st, 403)
st, _ = call(viewer, "GET", "/api/v1/files/read?path=/etc//shadow")
check("viewer cannot read /etc//shadow", st, 403)
st, _ = call(viewer, "GET", "/api/v1/files/read?path=/etc/./shadow")
check("viewer cannot read /etc/./shadow", st, 403)

# 2. The command drawer prints every argument Islet ever passed.
st, _ = call(viewer, "GET", "/api/v1/commands")
check("viewer cannot read the command drawer", st, 403)

# 3. Host logs are read as root.
st, _ = call(viewer, "GET", "/api/v1/logs/stream?source=journal&tail=5&follow=0")
check("viewer cannot tail the journal", st, 403)

# 4. Writing a compose file is root on the host.
st, _ = call(viewer, "PUT", "/api/v1/docker/stacks/evil", {"name": "evil", "compose": "services:\n  x:\n    image: alpine\n", "env": "", "isNew": True})
check("viewer cannot write a stack", st, 403)

# 5. A token may not outrank its owner.
st, body = call(viewer, "POST", "/api/v1/auth/tokens", {"name": "t", "scopes": "deploy", "ttlDays": 1})
check("viewer cannot mint a deploy token", st, 400)
st, body = call(viewer, "POST", "/api/v1/auth/tokens", {"name": "t2", "scopes": "shell", "ttlDays": 1})
check("viewer cannot mint a shell token", st, 400)

# 6. An admin's read-only token must not reach a shell.
st, body = call(admin, "POST", "/api/v1/auth/tokens", {"name": "ro", "scopes": "read", "ttlDays": 1})
token = json.loads(body).get("token", "") if st in (200, 201) else ""
print("admin read-only token ->", st)
if token:
    anon, _ = client()
    st, _ = call(anon, "GET", "/api/v1/terminal/ws", headers={"Authorization": "Bearer " + token})
    ok = st in (401, 403)
    print(("PASS " if ok else "FAIL ") + f"read-only token cannot open the terminal  (got {st})")
    if not ok:
        fails.append("read token terminal")
    st, _ = call(anon, "GET", "/api/v1/system", headers={"Authorization": "Bearer " + token})
    check("read-only token still reads", st, 200)

# 7. Secrets never reach the command log.
st, body = call(admin, "GET", "/api/v1/commands?limit=200")
leaked = [w for w in ("RESTIC_PASSWORD=", "AWS_SECRET_ACCESS_KEY=", "ACCESS_TOKEN=") if w + "<" not in body and w in body]
print(("PASS " if not leaked else "FAIL ") + "no secret values in the command log " + (str(leaked) if leaked else ""))
if leaked:
    fails.append("secret redaction")

# 8. Managing another server means root on that machine, and the proxy is a way
# to act as an administrator there. Neither is a viewer's to touch.
st, _ = call(viewer, "GET", "/api/v1/servers")
check("viewer cannot list the fleet", st, 403)
st, _ = call(viewer, "GET", "/api/v1/servers/key")
check("viewer cannot read the panel's ssh key", st, 403)
st, _ = call(viewer, "POST", "/api/v1/servers", {"name": "mine", "host": "203.0.113.9", "sshUser": "root", "sshPort": 22})
check("viewer cannot add a server", st, 403)
st, _ = call(viewer, "GET", "/api/v1/servers/anything/proxy/system")
check("viewer cannot reach another server through the proxy", st, 403)
st, _ = call(viewer, "DELETE", "/api/v1/servers/anything")
check("viewer cannot remove a server", st, 403)

# The proxy must not be talked into leaving the API it forwards.
st, _ = call(admin, "GET", "/api/v1/servers/anything/proxy/../../../etc/passwd")
ok = st in (400, 404)
print(("PASS " if ok else "FAIL ") + f"the proxy refuses a path that climbs out  (got {st})")
if not ok:
    fails.append("proxy path traversal")

print()
print("FAILURES:", fails if fails else "none")
sys.exit(1 if fails else 0)
