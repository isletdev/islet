// Assistant end-to-end: what happens to a run when the client goes away.
//
// Two failures this is written from, both reported from a phone and neither
// visible in any unit test:
//
//   1. Locking the screen dropped the connection, the page reported the dropped
//      fetch as "Failed to fetch", and the deploy it was reporting on was fine.
//   2. Coming back to a cold page mid-run showed "working" and then only the
//      tool calls that happened next — everything the run had already done was
//      missing until it finished.
//
// Both are about a browser, a network and a run that outlives them, so the only
// honest test drives all three. It needs an assistant configured on the target
// daemon, and a browser listening for the DevTools protocol:
//
//   chrome-headless-shell --headless --no-sandbox --remote-debugging-port=9222 about:blank
//   ISLET_BASE=http://127.0.0.1:9444 node hack/e2e-assistant.mjs "$SESSION_COOKIE"
//
// It exits non-zero on the first thing that is wrong.

const [, , cookie] = process.argv;
const BASE = process.env.ISLET_BASE ?? "http://127.0.0.1:9443";
const DEBUG = process.env.ISLET_DEBUG ?? "http://127.0.0.1:9222";
const QUESTION = "List the domains, the apps, the containers and the disk usage — one tool call at a time — then a one-line summary.";

if (!cookie) {
  console.error("usage: ISLET_BASE=… node hack/e2e-assistant.mjs <islet_session cookie>");
  process.exit(2);
}

const targets = await (await fetch(DEBUG + "/json/list")).json();
const page = targets.find((t) => t.type === "page");
const ws = new WebSocket(page.webSocketDebuggerUrl);
await new Promise((res, rej) => { ws.onopen = res; ws.onerror = rej; });
let id = 0; const pending = new Map();
ws.onmessage = (ev) => { const m = JSON.parse(ev.data); if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id); } };
const send = (method, params = {}) => { const n = ++id; ws.send(JSON.stringify({ id: n, method, params })); return new Promise((res, rej) => pending.set(n, (m) => (m.error ? rej(new Error(m.error.message)) : res(m.result)))); };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const failures = [];
const check = (ok, what) => {
  console.log(`${ok ? "  ok  " : "  FAIL"}  ${what}`);
  if (!ok) failures.push(what);
};

const look = async () => {
  const r = await send("Runtime.evaluate", { expression: `(() => {
    const tools = [...document.querySelectorAll("li")].map((e) => e.textContent.trim())
      .filter((t) => /^[✓✕◌]/.test(t)).map((t) => (t.match(/[a-z_]{4,}/) || [""])[0]);
    const errors = [...document.querySelectorAll("[role=alert]")].map((e) => e.textContent.trim()).filter(Boolean);
    return JSON.stringify({
      tools: [...new Set(tools)].filter(Boolean),
      errors,
      running: [...document.querySelectorAll("button")].some((b) => b.textContent.trim() === "Stop"),
      asked: document.body.innerText.includes("one tool call at a time"),
    });
  })()`, returnByValue: true });
  return JSON.parse(r.result.value);
};

await send("Page.enable"); await send("Runtime.enable"); await send("Network.enable");
await send("Network.setCookie", { name: "islet_session", value: cookie, domain: new URL(BASE).hostname, path: "/", httpOnly: true });
await send("Emulation.setDeviceMetricsOverride", { width: 390, height: 844, deviceScaleFactor: 1, mobile: true });

const goToAssistant = async () => {
  await send("Page.navigate", { url: BASE + "/assistant" });
  await sleep(3000);
  await send("Runtime.evaluate", { expression: `document.querySelectorAll('button[aria-label*="ismiss"],button[aria-label*="lose"]').forEach((b) => b.click())` });
};

await goToAssistant();
await send("Runtime.evaluate", { expression: `[...document.querySelectorAll("button")].find((b) => b.textContent.trim() === "New")?.click()` });
await sleep(400);
await send("Runtime.evaluate", { expression: `(() => {
  const el = document.querySelector("input[aria-label='Ask the assistant']");
  if (!el || el.disabled) throw new Error("no assistant is configured on this daemon");
  Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value").set.call(el, ${JSON.stringify(QUESTION)});
  el.dispatchEvent(new Event("input", { bubbles: true }));
})()` });
await sleep(300);
await send("Runtime.evaluate", { expression: `[...document.querySelectorAll("button")].find((b) => b.textContent.trim() === "Ask")?.click()` });
await sleep(10000);

const working = await look();
check(working.running, "the run is under way");
check(working.tools.length > 0, `tools are named as they run (${working.tools.join(", ") || "none"})`);
const before = working.tools.length;

// The screen locks: the connection goes without anything aborting it politely.
await send("Network.emulateNetworkConditions", { offline: true, latency: 0, downloadThroughput: 0, uploadThroughput: 0 });
await sleep(6000);
const dark = await look();
check(dark.errors.length === 0, `a dropped connection is not reported as a failure (saw: ${dark.errors.join(" | ") || "nothing"})`);

await send("Network.emulateNetworkConditions", { offline: false, latency: 0, downloadThroughput: -1, uploadThroughput: -1 });
await send("Runtime.evaluate", { expression: `window.dispatchEvent(new Event("online"))` });
await sleep(6000);
const back = await look();
check(back.errors.length === 0, "coming back shows no error");
check(back.tools.length >= before, `what already ran is still on screen (${back.tools.length} of ${before})`);

// And the harder one: a cold page, with none of this in memory, mid-run.
await send("Page.navigate", { url: "about:blank" });
await sleep(4000);
await goToAssistant();
await sleep(4000);
const cold = await look();
check(cold.asked, "a cold page shows the question again");
check(cold.errors.length === 0, "a cold page shows no error");
check(cold.tools.length >= before, `a cold page replays what already ran (${cold.tools.length} of ${before})`);

// Let it finish, so the end of the run is exercised too.
for (let i = 0; i < 30; i++) {
  const s = await look();
  if (!s.running) {
    check(s.errors.length === 0, "the finished run left no error behind");
    check(s.tools.length > 0, "the finished conversation still lists what it did");
    break;
  }
  await sleep(2000);
}

ws.close();
console.log(failures.length === 0 ? "\nassistant e2e: clean" : `\nassistant e2e: ${failures.length} failed`);
process.exit(failures.length === 0 ? 0 : 1);
