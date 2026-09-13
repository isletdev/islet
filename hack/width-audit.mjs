// Does an explicit width on a field actually hold? Tailwind decides between two
// width utilities by their order in the stylesheet, not by the order the class
// names are written, so a component default can quietly beat what the caller
// asked for. The only honest answer is to measure it in a real browser.
//
// Usage, with isletd running locally and a browser listening for the DevTools
// protocol:
//
//   msedge --headless=new --remote-debugging-port=9222 --user-data-dir=/tmp/p about:blank
//   node hack/width-audit.mjs "$SESSION_COOKIE" /security /domains /backups
//
// It exits zero either way and prints what it found; read the list.
const [, , cookie, ...paths] = process.argv;
const DEBUG = "http://127.0.0.1:9222", BASE = "http://127.0.0.1:9443";
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const list = await (await fetch(DEBUG + "/json/list")).json();
const ws = new WebSocket(list.find((t) => t.type === "page" && (t.url.startsWith(BASE) || t.url === "about:blank")).webSocketDebuggerUrl);
await new Promise((res, rej) => { ws.onopen = res; ws.onerror = rej; });
let id = 0; const pending = new Map();
ws.onmessage = (e) => { const m = JSON.parse(e.data); if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id); } };
const send = (method, params = {}) => { const n = ++id; ws.send(JSON.stringify({ id: n, method, params })); return new Promise((res, rej) => pending.set(n, (m) => (m.error ? rej(new Error(m.error.message)) : res(m.result)))); };
await send("Page.enable"); await send("Network.enable"); await send("Runtime.enable");
await send("Network.setCookie", { name: "islet_session", value: cookie, domain: "127.0.0.1", path: "/", httpOnly: true });
await send("Emulation.setDeviceMetricsOverride", { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });

const W = { 20: 80, 24: 96, 28: 112, 32: 128, 36: 144, 40: 160, 44: 176, 48: 192, 52: 208, 56: 224, 64: 256, 72: 288, 80: 320, 96: 384 };
const PROBE = `(() => {
  const W = ${JSON.stringify(W)};
  const out = [];
  for (const el of document.querySelectorAll("input, select, textarea")) {
    const cls = (el.getAttribute("class") || "");
    const m = cls.match(/(?:^|\\s)w-(\\d+)(?:\\s|$)/);
    if (!m) continue;
    const want = W[m[1]];
    const got = Math.round(el.getBoundingClientRect().width);
    if (want && Math.abs(got - want) > 2) {
      out.push({ want, got, cls: cls.slice(0, 90), name: el.name || el.placeholder || (el.previousElementSibling && el.previousElementSibling.textContent) || el.tagName });
    }
  }
  return out;
})()`;

let bad = 0;
for (const path of paths) {
  await send("Page.navigate", { url: BASE + path });
  await sleep(2300);
  const { result } = await send("Runtime.evaluate", { expression: PROBE, returnByValue: true });
  const v = result.value || [];
  if (!v.length) { console.log(path, "-> widths hold"); continue; }
  console.log(path);
  for (const f of v) { console.log("   want", f.want, "got", f.got, "|", f.name, "|", f.cls); bad++; }
}
ws.close();
console.log(bad ? `\n${bad} input(s) whose width class is being overridden.` : "\nNo overridden widths.");
