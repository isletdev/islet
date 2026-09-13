// A command's output has no natural end: a Docker build can run to tens of
// thousands of lines. Keeping all of them, and re-joining the whole array on
// every line, turns a long deploy into a frozen tab.
export const MAX_LOG_LINES = 4000;

/** Append a line, keeping only the most recent MAX_LOG_LINES. */
export function capLines(prev: string[] | null, line: string): string[] {
  const next = prev ?? [];
  if (next.length < MAX_LOG_LINES) return [...next, line];
  return [...next.slice(next.length - MAX_LOG_LINES + 1), line];
}
