// Islet service worker: caches the app shell so the panel opens instantly
// and shows a friendly page when the server is unreachable. API calls are
// never cached.
const SHELL = "islet-shell-v1";
const ASSETS = ["/", "/manifest.webmanifest", "/icon-192.png", "/icon-512.png"];

self.addEventListener("install", (e) => {
  e.waitUntil(caches.open(SHELL).then((c) => c.addAll(ASSETS)).catch(() => {}));
  self.skipWaiting();
});

self.addEventListener("activate", (e) => {
  e.waitUntil(caches.keys().then((keys) => Promise.all(keys.filter((k) => k !== SHELL).map((k) => caches.delete(k)))));
  self.clients.claim();
});

self.addEventListener("fetch", (e) => {
  const url = new URL(e.request.url);
  if (e.request.method !== "GET" || url.pathname.startsWith("/api/") || url.pathname.startsWith("/mcp") || url.pathname.startsWith("/_islet/")) return;
  // Hashed assets: cache first (they never change under the same name).
  if (url.pathname.startsWith("/assets/")) {
    e.respondWith(caches.open(SHELL).then(async (c) => (await c.match(e.request)) || fetch(e.request).then((res) => { if (res.ok) c.put(e.request, res.clone()); return res; })));
    return;
  }
  // Navigation: network first, fall back to the cached shell.
  if (e.request.mode === "navigate") {
    e.respondWith(fetch(e.request).then((res) => { caches.open(SHELL).then((c) => c.put("/", res.clone())); return res; }).catch(() => caches.match("/").then((r) => r || new Response("<h1>Islet is unreachable</h1><p>The server is not answering. Check that isletd is running.</p>", { headers: { "Content-Type": "text/html" } }))));
  }
});
