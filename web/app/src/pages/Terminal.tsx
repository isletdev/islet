import TermView from "@/components/TermView";
import { openConsole } from "@/pages/Console";
import { Button } from "@/components/ui";
import { ExternalIcon } from "@/components/icons";

export default function Terminal() {
  return (
    <div className="mx-auto flex h-full max-w-6xl flex-col">
      <div className="mb-3 flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-[-0.02em]">Terminal</h1>
          <p className="mt-1 text-ink-muted">A shell on this server as the daemon's user. Every session is written to the audit log.</p>
        </div>
        <Button variant="secondary" className="h-8 gap-2 px-2.5 text-xs" onClick={() => openConsole()}>
          <ExternalIcon className="h-4 w-4" />
          Open in a window
        </Button>
      </div>
      <TermView path="/api/v1/terminal/ws" className="flex-1" />
    </div>
  );
}
