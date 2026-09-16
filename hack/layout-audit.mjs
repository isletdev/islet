// Layout audit: drives a headless browser over the panel and reports the layout
// faults a person would call a bug. A scrollbar nobody asked for, content
// spilling past the viewport with nothing able to scroll to it, a control too
// small to hit.
//
// It exists because these only show at certain widths, so they survive review
// and land on the user instead. Two faults it has already caught: a tab strip
// that scrolled vertically by one pixel, and cards that pushed past a phone
// screen because a grid had no column template below its breakpoint.
//
// Usage, with isletd running locally and a browser listening for the DevTools
// protocol:
//
//   msedge --headless=new --remote-debugging-port=9222 --user-data-dir=/tmp/p about:blank
//   node hack/layout-audit.mjs "$SESSION_COOKIE" "/|390|844" "/settings|1440|900"
//
// Each argument after the cookie is a page as path|width|height. It exits
// non-zero when anything is found, so CI can fail on it.

const [, , cookie, ...paths] = process.argv;
const DEBUG = "http://127.0.0.1:9222";
// The installed daemon owns 9443 on a server Islet manages, so a build under
// test runs on another port. ISLET_BASE says which.
const BASE = process.env.ISLET_BASE ?? "http://127.0.0.1:9443";
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const list = await (await fetch(DEBUG + "/json/list")).json();
const page = list.find((t) => t.type === "page" && (t.url.startsWith(BASE) || t.url === "about:blank"));
const ws = new WebSocket(page.webSocketDebuggerUrl);
await new Promise((res, rej) => { ws.onopen = res; ws.onerror = rej; });
let id = 0; const pending = new Map();
ws.onmessage = (ev) => { const m = JSON.parse(ev.data); if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id); } };
const send = (method, params = {}) => { const n = ++id; ws.send(JSON.stringify({ id: n, method, params })); return new Promise((res, rej) => pending.set(n, (m) => (m.error ? rej(new Error(m.error.message)) : res(m.result)))); };

await send("Page.enable"); await send("Network.enable"); await send("Runtime.enable");
await send("Network.setCookie", { name: "islet_session", value: cookie, domain: new URL(BASE).hostname, path: "/", httpOnly: true });

const PROBE = `(() => {
  const out = [];
  const label = (el) => {
    const cls = (el.className && typeof el.className === "string") ? "." + el.className.trim().split(/\\s+/).slice(0, 4).join(".") : "";
    const txt = (el.textContent || "").trim().replace(/\\s+/g, " ").slice(0, 34);
    return el.tagName.toLowerCase() + cls + (txt ? " :: " + txt : "");
  };
  // Whether this element scrolls sideways on purpose.
  //
  // CSS says an element with overflow-y auto and overflow-x visible computes
  // its overflow-x to auto, so the app's scrolling main column looks exactly
  // like a deliberate horizontal scroller — and every field hanging off the
  // right-hand edge inside it was forgiven. That is how a settings input 448px
  // wide on a 390px phone passed this audit and reached a user instead.
  const deliberateX = (p, ps) => {
    const cls = typeof p.className === "string" ? p.className : "";
    // overflow-x-auto and overflow-auto are both somebody asking; overflow-y-auto
    // is not, and that is the whole distinction.
    if (/(^|\\s)overflow-(x-)?(auto|scroll)(\\s|$)/.test(cls)) return true;
    if (ps.overflowX === "scroll") return true;
    return ps.overflowX === "auto" && ps.overflowY !== "auto" && ps.overflowY !== "scroll";
  };
  for (const el of document.querySelectorAll("body *")) {
    const cs = getComputedStyle(el);
    if (cs.display === "none" || cs.visibility === "hidden") continue;
    const r = el.getBoundingClientRect();
    if (r.width === 0 || r.height === 0) continue;

    const scrollsY = el.scrollHeight - el.clientHeight;
    const scrollsX = el.scrollWidth - el.clientWidth;
    const canY = cs.overflowY === "auto" || cs.overflowY === "scroll";
    const canX = cs.overflowX === "auto" || cs.overflowX === "scroll";

    // A scroll container that overflows by a hair is an accident, not a design.
    if (canY && scrollsY > 0 && scrollsY <= 4) out.push({ kind: "stray-vertical-scroll", by: scrollsY, el: label(el) });
    if (canX && scrollsX > 0 && scrollsX <= 4) out.push({ kind: "stray-horizontal-scroll", by: scrollsX, el: label(el) });

    // Content wider than the viewport with no scroll container above it to
    // reach it. An element inside a horizontal scroller is meant to be there —
    // but only if somebody asked for a horizontal scroller.
    if (!canX && r.right > document.documentElement.clientWidth + 1 && el.children.length === 0) {
      let scrollable = false;
      for (let p = el.parentElement; p && p !== document.body; p = p.parentElement) {
        const ps = getComputedStyle(p);
        if (deliberateX(p, ps) && p.scrollWidth > p.clientWidth) { scrollable = true; break; }
      }
      if (!scrollable) out.push({ kind: "spills-past-viewport", by: Math.round(r.right - document.documentElement.clientWidth), el: label(el) });
    }

    // Interactive targets smaller than a fingertip.
    if ((el.tagName === "BUTTON" || el.tagName === "A" || el.tagName === "SELECT" || el.tagName === "INPUT") && cs.pointerEvents !== "none") {
      if (r.height > 0 && r.height < 20 && (el.textContent || "").trim().length > 0) {
        out.push({ kind: "tiny-hit-target", h: Math.round(r.height), el: label(el) });
      }
    }
  }
  const doc = document.documentElement;
  if (doc.scrollWidth > doc.clientWidth) out.push({ kind: "page-scrolls-sideways", by: doc.scrollWidth - doc.clientWidth, el: "html" });
  // De-duplicate identical findings.
  const seen = new Set();
  return out.filter((f) => { const k = f.kind + "|" + f.el + "|" + (f.by ?? f.h); if (seen.has(k)) return false; seen.add(k); return true; });
})()`;

const report = {};
for (const spec of paths) {
  const [path, w = "1440", h = "900"] = spec.split("|");
  await send("Emulation.setDeviceMetricsOverride", { width: +w, height: +h, deviceScaleFactor: 1, mobile: +w < 700 });
  await send("Page.navigate", { url: BASE + path });
  await sleep(2200);
  const { result } = await send("Runtime.evaluate", { expression: PROBE, returnByValue: true });
  report[`${path} @${w}`] = result.value;
}
ws.close();

let found = 0;
for (const [k, v] of Object.entries(report)) {
  if (!v.length) { console.log(k, "-> clean"); continue; }
  console.log(k);
  for (const f of v) { console.log("   ", f.kind, f.by ?? f.h ?? "", "|", f.el); found++; }
}
if (found) {
  console.error("");
  console.error(found + " layout fault(s).");
  process.exit(1);
}
console.log("");
console.log("No layout faults.");
