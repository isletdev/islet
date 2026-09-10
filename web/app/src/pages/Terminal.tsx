import TermView from "@/components/TermView";

export default function Terminal() {
  return (
    <div className="mx-auto flex h-[calc(100vh-6rem)] max-w-6xl flex-col">
      <div className="mb-3">
        <h1 className="text-xl font-semibold tracking-[-0.02em]">Terminal</h1>
        <p className="mt-1 text-ink-muted">A shell on this server as the daemon's user. Every session is written to the audit log.</p>
      </div>
      <TermView path="/api/v1/terminal/ws" className="flex-1" />
    </div>
  );
}
