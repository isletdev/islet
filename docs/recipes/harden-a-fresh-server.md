# Harden a fresh server

Ten minutes from a default image to a Security Score of 90.

1. **Settings, Two-factor.** Turn it on for your admin account (10 points).
2. **Security.** Press **Fix** on, in this order: unattended upgrades, fail2ban, swap, NTP, firewall. The firewall fix allows SSH, 80, 443 and the panel port, adds your current IP, and installs the Docker-aware rules so published container ports honour it.
3. **SSH.** Make sure your public key is in `authorized_keys` (the card says whether one was found). Then untick password authentication and root password login, **Apply with 5-minute rollback**, open a *new* SSH session to prove you still get in, and press **Confirm**.
4. **Databases.** If any instance shows a public port you do not need, stop publishing it. Use an SSH tunnel or Tailscale instead.
5. **Backups.** Add a destination and a plan; a server without backups caps the score.
6. Optional: install **Tailscale** or **WireGuard** from the catalog and allow the panel port only from the VPN range, then remove the public rule.
