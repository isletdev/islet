import { useEffect, useRef, useState } from "react";
import { Terminal as XTerm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "@xterm/xterm/css/xterm.css";
import { Button } from "@/components/ui";

export type TermStatus = "connecting" | "open" | "closed" | "error";

/** xterm.js bound to a WebSocket PTY endpoint (host terminal or container exec). */
export default function TermView({ path, className = "" }: { path: string; className?: string }) {
  const host = useRef<HTMLDivElement>(null);
  const [status, setStatus] = useState<TermStatus>("connecting");
  const [gen, setGen] = useState(0);

  useEffect(() => {
    const el = host.current;
    if (!el) return;
    const term = new XTerm({
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
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.loadAddon(new WebLinksAddon());
    term.open(el);
    fit.fit();

    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const ws = new WebSocket(`${proto}//${location.host}${path}`);
    ws.binaryType = "arraybuffer";
    setStatus("connecting");
    const sendResize = () => { if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows })); };
    ws.onopen = () => { setStatus("open"); sendResize(); term.focus(); };
    ws.onmessage = (ev) => term.write(new Uint8Array(ev.data as ArrayBuffer));
    ws.onclose = () => { setStatus("closed"); term.write("\r\n\x1b[90m[session closed]\x1b[0m\r\n"); };
    ws.onerror = () => setStatus("error");
    const enc = new TextEncoder();
    const onData = term.onData((d) => { if (ws.readyState === WebSocket.OPEN) ws.send(enc.encode(d)); });
    const onResize = term.onResize(sendResize);
    const ro = new ResizeObserver(() => fit.fit());
    ro.observe(el);
    return () => { ro.disconnect(); onData.dispose(); onResize.dispose(); ws.close(); term.dispose(); };
  }, [path, gen]);

  return (
    <div className={`flex min-h-0 flex-col ${className}`}>
      <div className="mb-2 flex items-center justify-end gap-3 text-xs text-ink-muted">
        <span>{status === "open" ? "Connected" : status === "connecting" ? "Connecting…" : status === "closed" ? "Closed" : "Connection failed"}</span>
        {(status === "closed" || status === "error") && <Button variant="secondary" className="h-7 px-2 text-xs" onClick={() => setGen((g) => g + 1)}>Reconnect</Button>}
      </div>
      <div ref={host} className="min-h-0 flex-1 overflow-hidden rounded-lg border border-border bg-[#0A0A0A] p-2" />
    </div>
  );
}
