# Vaultwarden password manager

1. **Apps, Catalog, Vaultwarden.** Domain with Let's Encrypt is required (browser extensions refuse plain HTTP). The admin token is generated; find it under Installed, Credentials.
2. Open `https://vault.example.com/admin`, paste the token, set SMTP so invitations and 2FA mail work (see the SMTP relay in Notifications, or any provider).
3. Create your account, then turn off open signups in the admin panel.
4. **Backups:** `volume vaultwarden_data` nightly to an off-site destination, and run a restore test once.
