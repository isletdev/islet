/**
 * WireGuard keys, made in the browser.
 *
 * A device's private key is generated here, put into a QR code and a file, and
 * forgotten. Only the public half is sent to the server. That is the property
 * the VPN page is built on: the server — and anybody who copies its database —
 * never holds a key that could join the tunnel.
 *
 * WebCrypto has X25519 in every current browser, but only in a secure
 * context, and a panel opened over plain HTTP on an address is not one. So
 * there is a fallback: the RFC 7748 ladder in BigInt, which is short enough to
 * read in one sitting. It is not constant-time, which matters for a key used
 * in a long-running exchange an attacker can time and not for one computed
 * once, on the owner's own machine, to be copied into a phone.
 */

export interface WgKeypair {
  privateKey: string;
  publicKey: string;
}

const b64 = (b: Uint8Array) => btoa(String.fromCharCode(...b));

export async function generateKeypair(): Promise<WgKeypair> {
  const subtle = globalThis.crypto?.subtle;
  if (subtle) {
    try {
      const kp = (await subtle.generateKey({ name: "X25519" }, true, ["deriveBits"])) as CryptoKeyPair;
      const pub = new Uint8Array(await subtle.exportKey("raw", kp.publicKey));
      // PKCS#8 for X25519 is a fixed 16-byte header and then the 32-byte key.
      const pkcs8 = new Uint8Array(await subtle.exportKey("pkcs8", kp.privateKey));
      return { privateKey: b64(pkcs8.slice(-32)), publicKey: b64(pub) };
    } catch {
      // An older engine without X25519: fall through to the ladder.
    }
  }
  const priv = new Uint8Array(32);
  globalThis.crypto.getRandomValues(priv);
  return { privateKey: b64(priv), publicKey: b64(x25519Base(priv)) };
}

/** The public half of a private key: what `wg pubkey` prints. */
export function publicFromPrivate(privB64: string): string {
  const raw = Uint8Array.from(atob(privB64), (c) => c.charCodeAt(0));
  return b64(x25519Base(raw));
}

// ---- RFC 7748, section 5 ----

const P = (1n << 255n) - 19n;
const A24 = 121665n;
const mod = (a: bigint) => { const r = a % P; return r < 0n ? r + P : r; };

function pow(b: bigint, e: bigint): bigint {
  let r = 1n;
  b = mod(b);
  while (e > 0n) {
    if (e & 1n) r = (r * b) % P;
    b = (b * b) % P;
    e >>= 1n;
  }
  return r;
}

function x25519Base(k: Uint8Array): Uint8Array {
  const s = k.slice();
  s[0] &= 248;
  s[31] &= 127;
  s[31] |= 64;
  let scalar = 0n;
  for (let i = 31; i >= 0; i--) scalar = (scalar << 8n) | BigInt(s[i]);
  const x1 = 9n;
  let x2 = 1n, z2 = 0n, x3 = x1, z3 = 1n, swap = 0n;
  for (let t = 254; t >= 0; t--) {
    const kt = (scalar >> BigInt(t)) & 1n;
    swap ^= kt;
    if (swap) { [x2, x3] = [x3, x2]; [z2, z3] = [z3, z2]; }
    swap = kt;
    const a = mod(x2 + z2), aa = mod(a * a);
    const b = mod(x2 - z2), bb = mod(b * b);
    const e = mod(aa - bb);
    const c = mod(x3 + z3), d = mod(x3 - z3);
    const da = mod(d * a), cb = mod(c * b);
    x3 = mod((da + cb) * (da + cb));
    z3 = mod(x1 * mod((da - cb) * (da - cb)));
    x2 = mod(aa * bb);
    z2 = mod(e * mod(aa + A24 * e));
  }
  if (swap) { [x2, x3] = [x3, x2]; [z2, z3] = [z3, z2]; }
  let out = mod(x2 * pow(z2, P - 2n));
  const bytes = new Uint8Array(32);
  for (let i = 0; i < 32; i++) { bytes[i] = Number(out & 255n); out >>= 8n; }
  return bytes;
}

/** The configuration file a WireGuard app imports. */
export function clientConfig(o: {
  privateKey: string;
  address: string;
  presharedKey: string;
  serverPublicKey: string;
  endpoint: string;
  allowedIps: string[];
  dns?: string;
}): string {
  const lines = ["[Interface]", `PrivateKey = ${o.privateKey}`, `Address = ${o.address}/32`];
  if (o.dns) lines.push(`DNS = ${o.dns}`);
  lines.push(
    "",
    "[Peer]",
    `PublicKey = ${o.serverPublicKey}`,
    `PresharedKey = ${o.presharedKey}`,
    `Endpoint = ${o.endpoint}`,
    `AllowedIPs = ${o.allowedIps.join(", ")}`,
    // A phone behind NAT goes silent otherwise, and the server cannot reach
    // it until it speaks again.
    "PersistentKeepalive = 25",
    "",
  );
  return lines.join("\n");
}
