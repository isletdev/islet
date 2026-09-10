# Static site with a domain

For Vite, Create React App, Astro, Angular, Gatsby or plain HTML.

1. **Apps, Your apps, New app.** Name it, paste the repository URL, keep the branch. Click **Detect**: Islet clones the repo and says what it found ("Vite + React app. Build: npm run build. Output: dist/"). Change anything that is wrong.
2. **Domain.** Type your domain, or leave it empty for a free preview address on sslip.io. For your own domain, add an A record pointing at the server's IP first; the Domains page has a DNS helper that turns green when it resolves.
3. **Create app**, then **Deploy**. Watch the log: clone, generated Dockerfile, build, health check, route. The site is served by nginx with SPA fallback, gzip and long cache headers on hashed assets. A `_redirects` file (Netlify style) is honoured.
4. **Auto-deploy.** Open the Auto-deploy panel, copy the webhook URL and secret into the repository's webhook settings (content type JSON). Every push to the branch redeploys; Releases shows each one with a Roll back action.

Optional: password-protect a staging site from Domains, the domain, Basic auth.
