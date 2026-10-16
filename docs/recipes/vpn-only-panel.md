# A VPN, and the panel only inside it

WireGuard on the server itself, your phone and laptop on it in a minute each,
and then the panel and SSH closed to everybody who is not. Nobody on the
internet can try a password on a door they cannot reach.

1. **VPN, Turn on.** The endpoint is filled in with the server's public
   address. If you would rather use a name, it must point straight at the
   server: a CDN in front (Cloudflare's orange cloud) passes web traffic and
   drops WireGuard's. One UDP port opens in the firewall; nothing else changes.
2. **Add device.** Name it, leave the access at *Admin*, press **Add**. The key
   is made in your browser — the server only ever sees the public half — and
   you get a QR code. In the WireGuard app, add a tunnel and scan it. The code
   is shown once; a lost one is fixed with **New key**, not by finding it again.
3. **Connect it,** and watch the dot next to the device turn green.
4. **Lockdown.** Leave *The panel* and *SSH* ticked and press **Turn on
   lockdown**. Your browser is cut off at once unless it is on the VPN. On the
   connected device, open `http://10.66.0.1` (the page shows the exact
   address), sign in, and press **Confirm** within two minutes. Unconfirmed, it
   puts everything back on its own — so a mistake costs two minutes, not a
   rescue console.

## The server, from your laptop

Inside the tunnel `10.66.0.1` *is* the server, loopback included:

- **The panel** is at `http://10.66.0.1`. Plain HTTP is right here: WireGuard
  encrypts and authenticates every packet, and there is no certificate
  warning to click through. Everything works over it — copy buttons, the
  terminal, sign-in. The same port refuses plain HTTP to anybody outside the
  tunnel and sends them to HTTPS.
- **A database for development** goes on 127.0.0.1: on its page under
  Databases, publish the port and choose *This server and the VPN*. Your
  laptop connects to `10.66.0.1:5433` (or whichever port); the internet
  reaches nothing, and no firewall rule was opened for it. The same holds for
  anything else that listens on 127.0.0.1 — Redis, an app's admin port, a dev
  server.
- **The VPN page lists them**, under *Reach this server*: every port a device
  in the tunnel can open, which container is behind it, and whether it also
  listens on the internet.

Other devices get narrower access:

- **Apps** reaches only the ports you list — 80 and 443 reach every site on
  the server, including ones with no public DNS at all, and 5432 a database
  kept on 127.0.0.1.
- **Internet only** browses through the server and reaches nothing on it: a
  travel VPN for a family member that is no way into your panel.
- **Expires** takes a device out of the tunnel on the date you give, for a
  contractor or a guest.

**Servers in the fleet** join from the same page. The panel then manages them
through the tunnel, and their SSH can be closed to the internet with their own
lockdown. If the tunnel is down the panel falls back to the public address.

**Moving from wg-easy** is one button on the VPN page while it is off. It takes
over wg-easy's server key, port and addresses, so every phone already set up
keeps working without being touched. wg-easy is stopped; remove the app once
you are happy.
