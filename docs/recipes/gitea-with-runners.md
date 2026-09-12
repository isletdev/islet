# Gitea with CI runners

1. **Apps, Catalog, Gitea.** Domain, Let's Encrypt. Finish the web installer (SQLite is fine for small teams).
2. In Gitea: Site administration, Actions, Runners, Create new runner, copy the registration token.
3. **Runners, New pool**: provider Gitea, URL `https://git.example.com`, paste the token, one long-lived runner. Workflows in `.gitea/workflows/` with `runs-on: ubuntu-latest` run on it.
4. Deploy from Gitea: on the app's Auto-deploy panel copy the webhook, add it in the repository settings as a Gitea webhook with the secret. Set "Deploy on: after CI passes" and call the hook from the last workflow step if you want tests to gate deploys.
