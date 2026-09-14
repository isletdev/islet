import TermView from "@/components/TermView";
import { openConsole } from "@/pages/Console";
import { Button } from "@/components/ui";
import { ExternalIcon } from "@/components/icons";

export default function Terminal() {
  return (
    <div className="mx-auto flex h-full max-w-6xl flex-col">
      <div className="mb-2 flex flex-wrap items-start justify-between gap-2 sm:mb-3 sm:gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-[-0.02em]">Terminal</h1>
          {/* A phone has 844px and the shell wants all of them: the heading,
              this sentence and the button were taking 255 of them before the
              terminal began. The sentence explains the page once and is worth
              nothing on every later visit, so it stays for the width that can
              afford it. */}
          <p className="mt-1 hidden text-ink-muted sm:block">A shell on this server as the daemon's user. Every session is written to the audit log.</p>
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
