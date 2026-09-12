# Move to a new server (or restore after losing one)

1. On the old server: **Backups**, make sure a plan includes Islet state, every volume that matters and the database instances, and that it ran recently. **Download recovery kit** and keep the file somewhere safe; it holds the repository credentials and keys.
2. On the new server (fresh Ubuntu 22.04+ or Debian 12+), as root:
   ```sh
   curl -fsSL https://raw.githubusercontent.com/isletdev/islet/main/installer/restore.sh -o restore.sh
   sh restore.sh recovery-kit.json            # optional: destination name, snapshot id
   ```
   It installs Docker and Islet, restores `/var/lib/islet`, recreates the volumes, starts the managed stacks and prints what is left.
3. Open the panel on the new address with the old credentials. Deployed apps show their releases; press **Deploy** on each (images are rebuilt, not backed up). Load database dumps with **Restore into a new instance** if the instance's volume was not in the plan.
4. Point DNS at the new server. Certificates are issued again automatically once the records resolve.
5. When everything answers, remove the old server; the backup repository stays valid for both.
