# Nightly backups off-site

1. Pick a destination. Cheapest reliable options: a Hetzner Storage Box (SFTP) or Backblaze B2 / Cloudflare R2 (S3-compatible). Create write-capable credentials there.
2. **Backups, Add destination.** Fill in the fields; Islet connects and initialises an encrypted repository with a key it generates.
3. **New plan.** Preset "Everything nightly", sources: Islet state, every database instance, the volumes of the apps you care about. Create it, then **Back up now** once to see it work.
4. **Download the recovery kit** and store it somewhere that is not this server. Without it, the encrypted repository cannot be read after the server is gone.
5. Restores: **Snapshots** on the destination, pick a snapshot, browse to a volume or folder, **Restore**. Volumes restore into a new volume; live data is never overwritten.
