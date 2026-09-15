import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { Terminal as XTerm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "@xterm/xterm/css/xterm.css";
import { Button, Input } from "@/components/ui";
import { apiPath } from "@/lib/api";

export type TermStatus = "connecting" | "open" | "closed" | "error";

/**
 * xterm.js bound to a WebSocket PTY endpoint — the host terminal, a container
 * exec, or a workspace.
 *
 * The terminal and the socket are deliberately separate. They used to live in
 * one effect, so reconnecting disposed the terminal and wiped everything on
 * screen; against a workspace, where the session on the other end is still
 * running, that threw away the only record of what had happened while you were
 * gone. The terminal is created once and the socket is replaced under it, so a
 * reconnect redraws into the same scrollback.
 *
 * `reattaches` is true for endpoints that join something already running. Those
 * can reconnect on their own, because doing so costs nothing and the screen
 * comes back as it was. A plain shell must not: a dropped connection there has
 * already killed the process, and silently opening a second one would leave
 * somebody typing into a fresh shell believing it was the old one.
 */
export default function TermView({
  path,
  className = "",
  reattaches = false,
}: {
  path: string;
  className?: string;
  reattaches?: boolean;
}) {
  const host = useRef<HTMLDivElement>(null);
  const term = useRef<XTerm | null>(null);
  const fit = useRef<FitAddon | null>(null);
  const sock = useRef<WebSocket | null>(null);
  const [status, setStatus] = useState<TermStatus>("connecting");
  const [gen, setGen] = useState(0);
  const [attempt, setAttempt] = useState(0);
  const [menu, setMenu] = useState<{ x: number; y: number } | null>(null);
  const [hasSel, setHasSel] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  // The manual paste box, for browsers that refuse to read the clipboard.
  const [pasteOpen, setPasteOpen] = useState(false);
  const [pasteText, setPasteText] = useState("");

  // The terminal itself, created once and kept across reconnects.
  useEffect(() => {
    const el = host.current;
    if (!el) return;
    const t = new XTerm({
      cursorBlink: true,
      fontFamily: "Geist Mono, ui-monospace, Menlo, Consolas, monospace",
      fontSize: 13,
      lineHeight: 1.2,
      scrollback: 5000,
      theme: {
        background: "#0A0A0A", foreground: "#FAFAFA", cursor: "#FAFAFA", selectionBackground: "#3A3A3A",
        black: "#0A0A0A", red: "#F87171", green: "#4ADE80", yellow: "#FBBF24", blue: "#4D8DFF", magenta: "#C084FC", cyan: "#67E8F9", white: "#E5E5E5",
        brightBlack: "#6B6B6B", brightRed: "#FCA5A5", brightGreen: "#86EFAC", brightYellow: "#FDE68A", brightBlue: "#93C5FD", brightMagenta: "#D8B4FE", brightCyan: "#A5F3FC", brightWhite: "#FFFFFF",
      },
    });
    const f = new FitAddon();
    t.loadAddon(f);
    t.loadAddon(new WebLinksAddon());
    t.open(el);
    f.fit();
    term.current = t;
    fit.current = f;

    // Copy and paste in a terminal.
    //
    // Ctrl+C cannot simply be copy: in a terminal it is interrupt, and taking
    // that away would leave no way to stop a running command — which matters
    // most in exactly the sessions worth leaving open. So it copies only when
    // there is a selection to copy, and is passed through untouched otherwise.
    // That is what Windows Terminal does, and what people already expect.
    //
    // Ctrl+V has no meaning to a shell, so it is always paste. macOS uses Cmd
    // for both, where there is no conflict at all. Ctrl+Shift+C/V work too, for
    // anyone with the habit.
    t.attachCustomKeyEventHandler((e) => {
      if (e.type !== "keydown") return true;
      const mac = /Mac|iPhone|iPad/.test(navigator.platform);
      const mod = mac ? e.metaKey : e.ctrlKey;
      if (!mod) return true;
      if (e.key.toLowerCase() !== "c") return true;
      // Paste is deliberately absent here. xterm keeps a hidden textarea and
      // the browser's own paste event delivers into it, so Ctrl+V and Cmd+V
      // already work — handling them here as well pasted everything twice.
      // The only paste that needs code is the one from the context menu, where
      // there is no native event to ride on.
      if (!t.hasSelection()) return true; // nothing to copy: let SIGINT through
      void copy(t.getSelection());
      // Clear it, so the next Ctrl+C interrupts rather than copying the same
      // text again. Otherwise a stray selection quietly disables interrupt.
      t.clearSelection();
      return false;
    });

    const onSel = t.onSelectionChange(() => setHasSel(t.hasSelection()));

    const enc = new TextEncoder();
    const onData = t.onData((d) => {
      const ws = sock.current;
      if (ws && ws.readyState === WebSocket.OPEN) ws.send(enc.encode(d));
    });
    const onResize = t.onResize(() => {
      const ws = sock.current;
      if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: "resize", cols: t.cols, rows: t.rows }));
    });
    const ro = new ResizeObserver(() => f.fit());
    ro.observe(el);
    return () => {
      ro.disconnect();
      onSel.dispose();
      onData.dispose();
      onResize.dispose();
      t.dispose();
      term.current = null;
      fit.current = null;
    };
  }, []);

  // Scrolling with a finger.
  //
  // xterm's viewport scrolls to a wheel, and a phone has no wheel. The screen
  // layer is drawn over the viewport, so a drag lands on an element that does
  // not scroll and nothing moves: on a touch device the output above the fold
  // was simply unreachable, which on a long agent session is most of it.
  //
  // The drag is turned into whole lines, and each line is dispatched as a wheel
  // event rather than passed to scrollLines. That is the difference between
  // scrolling a shell and scrolling the program running in it. xterm does three
  // different things with a wheel, and only it knows which applies:
  //
  //   normal buffer          moves through the scrollback
  //   alternate screen,      sends the mouse-report escape the program asked
  //     mouse reporting on   for, and the program scrolls itself
  //   alternate screen,      sends cursor up or down
  //     mouse reporting off
  //
  // Any full-screen program — an agent, vim, less — is on the alternate screen,
  // which has no scrollback at all, so scrollLines had nothing to move and did
  // nothing. That is why this worked in a shell and not inside Claude. One
  // event per line because the alternate-screen branch emits a single keypress
  // per wheel event whatever its magnitude.
  //
  // The event is dispatched at the finger, like a real wheel, so it reaches the
  // same listener by the same path. preventDefault is called only once a line
  // has moved, so a tap still focuses the terminal and raises the keyboard, and
  // a drag the terminal cannot use still scrolls the page.
  useEffect(() => {
    const el = host.current;
    const t = term.current;
    if (!el || !t) return;
    let lastY = 0;
    let carry = 0;
    const start = (e: TouchEvent) => {
      if (e.touches.length !== 1) return;
      lastY = e.touches[0].clientY;
      carry = 0;
    };
    const move = (e: TouchEvent) => {
      if (e.touches.length !== 1) return;
      const y = e.touches[0].clientY;
      carry += lastY - y;
      lastY = y;
      const rowHeight = el.clientHeight / Math.max(1, t.rows);
      const lines = Math.trunc(carry / rowHeight);
      if (lines === 0) return;
      carry -= lines * rowHeight;
      if (t.buffer.active.type === "normal") {
        // There is a scrollback, so move through it directly. A synthetic wheel
        // event cannot be used here: xterm lets the browser scroll its viewport
        // natively, and an event made in script carries no default action, so
        // dispatching one moves nothing at all.
        t.scrollLines(lines);
      } else {
        // The alternate screen has no scrollback to move, which is why calling
        // scrollLines here did nothing and the output inside a full-screen
        // program stayed unreachable. xterm's own wheel handler is what knows
        // whether the program asked for mouse reports or wants cursor keys, so
        // the movement is given to it as a wheel, at the finger, exactly where
        // a real one would land.
        const x = e.touches[0].clientX;
        const target = document.elementFromPoint(x, y) ?? el;
        const step = lines < 0 ? -rowHeight : rowHeight;
        // The position goes on the event because a mouse report carries the
        // cell it happened over, and a program with more than one pane scrolls
        // the one under the pointer. Without it every report reads as 1;1.
        // Capped: a flick can cover a lot of rows, and a program fed several
        // hundred scroll events at once behaves worse than one that scrolls less.
        for (let i = 0; i < Math.min(Math.abs(lines), 30); i++) {
          target.dispatchEvent(new WheelEvent("wheel", { deltaY: step, clientX: x, clientY: y, bubbles: true, cancelable: true }));
        }
      }
      e.preventDefault();
    };
    el.addEventListener("touchstart", start, { passive: true });
    el.addEventListener("touchmove", move, { passive: false });
    return () => {
      el.removeEventListener("touchstart", start);
      el.removeEventListener("touchmove", move);
    };
  }, []);

  // The socket, replaced on every reconnect.
  useEffect(() => {
    const t = term.current;
    if (!t) return;
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const ws = new WebSocket(`${proto}//${location.host}${apiPath(path)}`);
    ws.binaryType = "arraybuffer";
    sock.current = ws;
    setStatus("connecting");

    let retry: ReturnType<typeof setTimeout> | undefined;
    ws.onopen = () => {
      setStatus("open");
      setAttempt(0);
      ws.send(JSON.stringify({ type: "resize", cols: t.cols, rows: t.rows }));
      t.focus();
    };
    ws.onmessage = (ev) => t.write(new Uint8Array(ev.data as ArrayBuffer));
    ws.onerror = () => setStatus("error");
    ws.onclose = () => {
      setStatus("closed");
      if (!reattaches) {
        t.write("\r\n\x1b[90m[session closed]\x1b[0m\r\n");
        return;
      }
      // Backing off to 15s: the session is safe on the other side, so trying
      // forever is fine, but hammering a server that is restarting is not.
      const wait = Math.min(15000, 500 * 2 ** attempt);
      t.write(`\r\n\x1b[90m[reconnecting in ${Math.round(wait / 1000)}s]\x1b[0m\r\n`);
      retry = setTimeout(() => { setAttempt((n) => n + 1); setGen((g) => g + 1); }, wait);
    };
    return () => {
      if (retry) clearTimeout(retry);
      ws.onclose = null;
      ws.close();
      sock.current = null;
    };
    // attempt is read for the backoff but must not re-open the socket by itself.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path, gen, reattaches]);

  const copy = useCallback(async (text: string) => {
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      // Refused, usually because the panel is on plain http, where the
      // clipboard API is not available. Say which, rather than nothing.
      setNote("The browser would not let the page write to the clipboard. Use Ctrl+Shift+C, or serve the panel over HTTPS.");
      setTimeout(() => setNote(null), 6000);
    }
  }, []);

  const send = useCallback((text: string) => {
    const ws = sock.current;
    if (!ws || ws.readyState !== WebSocket.OPEN || !text) return;
    ws.send(new TextEncoder().encode(text));
    // Clicking a control took focus off the terminal, and a terminal you have
    // just pasted into is one you are about to type into.
    term.current?.focus();
  }, []);

  const paste = useCallback(async () => {
    const ws = sock.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    try {
      send(await navigator.clipboard.readText());
    } catch {
      // Reading the clipboard is refused far more often than writing it: Safari
      // and Firefox do not offer readText to a page at all, and no browser does
      // over plain http. Telling someone to press Ctrl+Shift+V is no help on a
      // phone, which has no Ctrl. So the page stops trying to take the
      // clipboard and offers somewhere to put it instead — the one paste every
      // platform allows is the one the person performs themselves.
      setPasteOpen(true);
    }
  }, [send]);

  // Closing the menu always hands the keyboard back. Paste does its own
  // focusing after the clipboard read resolves, so it is not closed here.
  const close = useCallback(() => { setMenu(null); term.current?.focus(); }, []);

  const reconnect = useCallback(() => { setAttempt(0); setGen((g) => g + 1); }, []);

  return (
    <div className={`flex min-h-0 flex-col ${className}`}>
      {/* Copy and paste are buttons as well as a context menu. A phone has no
          right-click and no Ctrl, so the menu these used to live in exclusively
          could not be opened at all, which left no way to paste on the device
          where typing a long command is hardest. */}
      <div className="mb-1.5 flex flex-wrap items-center gap-1.5 text-xs text-ink-muted sm:mb-2 sm:gap-2">
        {/* A dot and a word, rather than a word on its own at one end of an
            otherwise empty row: on a phone that read as a stray label instead
            of the state of the connection. */}
        <span className="mr-auto flex items-center gap-1.5">
          <span
            aria-hidden
            className={`h-1.5 w-1.5 shrink-0 rounded-full ${status === "open" ? "bg-success" : status === "connecting" ? "bg-warning" : "bg-danger"}`}
          />
          {status === "open" ? "Connected" : status === "connecting" ? "Connecting…" : status === "closed" ? "Closed" : "Connection failed"}
        </span>
        <Button variant="secondary" className="h-7 px-2 text-xs" disabled={!hasSel} onClick={() => void copy(term.current?.getSelection() ?? "")}>Copy</Button>
        <Button variant="secondary" className="h-7 px-2 text-xs" disabled={status !== "open"} onClick={() => void paste()}>Paste</Button>
        {(status === "closed" || status === "error") && (
          <Button variant="secondary" className="h-7 px-2 text-xs" onClick={reconnect}>Reconnect</Button>
        )}
      </div>
      {/*
        The padding and the border are on the outside, and the terminal's own
        parent carries neither.
        
        FitAddon works out how many rows fit from getComputedStyle(parent).height
        and then subtracts the padding of the *terminal* element, not the
        parent's. With box-sizing: border-box — which is every element here —
        that height includes the parent's padding and border, so a box 670px
        tall with 8px of padding and a 1px border reported 670 where 652 was
        usable: 37 rows asked for, 36 that fit, and the last one clipped to five
        of its eighteen pixels. Which is what it looked like: half a line at the
        bottom, on every terminal in the panel.
      */}
      <div className="min-h-0 flex-1 overflow-hidden rounded-lg border border-border bg-[#0A0A0A] p-2">
        <div
          ref={host}
          className="h-full w-full"
          // The browser's own menu here offers Back, Reload, View source and
          // Inspect — nothing that applies to a terminal, and nothing the two
          // things people actually want. This is those two things.
          onContextMenu={(e) => { e.preventDefault(); setMenu({ x: e.clientX, y: e.clientY }); }}
        />
      </div>
      {menu && (
        <>
          <div className="fixed inset-0 z-40" onClick={close} onContextMenu={(e) => { e.preventDefault(); close(); }} />
          <div
            role="menu"
            className="fixed z-50 min-w-40 rounded-md border border-border bg-surface py-1 text-sm shadow-float"
            style={{ left: Math.min(menu.x, window.innerWidth - 180), top: Math.min(menu.y, window.innerHeight - 160) }}
          >
            <MenuItem disabled={!hasSel} onClick={() => { void copy(term.current?.getSelection() ?? ""); close(); }}>
              Copy
            </MenuItem>
            <MenuItem onClick={() => { void paste(); setMenu(null); }}>Paste</MenuItem>
            <MenuItem onClick={() => { term.current?.selectAll(); setHasSel(true); close(); }}>Select all</MenuItem>
            <MenuItem onClick={() => { term.current?.clear(); close(); }}>Clear</MenuItem>
          </div>
        </>
      )}
      {pasteOpen && (
        <form
          className="mt-2 grid grid-cols-1 gap-2 sm:grid-cols-[minmax(0,1fr)_auto_auto]"
          onSubmit={(e) => { e.preventDefault(); send(pasteText); setPasteText(""); setPasteOpen(false); }}
        >
          <Input
            autoFocus
            value={pasteText}
            onChange={(e) => setPasteText(e.target.value)}
            placeholder="Paste here, then Send"
            className="font-mono"
            aria-label="Text to send to the terminal"
          />
          <Button type="submit" className="h-9 text-xs">Send</Button>
          <Button type="button" variant="secondary" className="h-9 text-xs" onClick={() => { setPasteOpen(false); setPasteText(""); term.current?.focus(); }}>Cancel</Button>
        </form>
      )}
      {note && <p className="mt-2 text-xs text-warning">{note}</p>}
    </div>
  );
}

function MenuItem({ children, onClick, disabled = false }: { children: ReactNode; onClick: () => void; disabled?: boolean }) {
  return (
    <button
      type="button"
      role="menuitem"
      disabled={disabled}
      onClick={onClick}
      className="block w-full px-3 py-1.5 text-left hover:bg-surface-2 disabled:cursor-default disabled:text-ink-faint disabled:hover:bg-transparent"
    >
      {children}
    </button>
  );
}
