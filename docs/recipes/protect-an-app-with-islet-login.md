# Protect an app with the Islet login

A fence in front of the door: the request has to carry an Islet session before
it reaches the app at all. The app keeps its own login — this only stops the
outside world from ever seeing it.

1. **Settings, Sessions, session cookie domain.** Set it to the parent both
   names share, e.g. `example.com` for a panel on `panel.example.com` and an app
   on `admin.example.com`. Without this the browser never sends the panel's
   cookie to the app's name and the login loops. A name outside that parent
   cannot be protected at all; the panel says so when you tick the box.
2. **Domains, edit the host, Protection.** The grid has one row per route — the
   site itself, then each custom location — and one column per Islet account.
3. **Whole site → Islet login.** Leave every tick empty and any signed-in Islet
   user gets through. Tick names and only those accounts do; anyone else signed
   in is told which account they are using and who to ask.
4. **A path that should be narrower.** Set that location to *Islet login* and
   tick fewer people. `/admin` can be two names on a site the rest of the team
   can reach.
5. **A path that must stay open.** Set it to *Open to anyone*. A payment
   callback or a webhook receiver has no browser and no session to offer, so on
   a protected host it needs this or it gets a redirect it cannot follow.
6. **Check it.** Open the site in a private window: you should land on the
   panel's login and come back to the page you asked for. Signed in as somebody
   not on the list, you get a page saying so rather than a loop.

**Where it does not apply.** Islet's login protects names under the session
cookie domain. For a name in another zone, run a panel on a name inside that
zone, or use basic auth or the IP allowlist on the same form.
