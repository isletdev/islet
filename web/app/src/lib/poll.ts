// Polling that stops while nobody is looking.
//
// Every page refreshes itself on a timer. Left open overnight, a single
// Containers tab used to issue thousands of "docker stats" calls against a
// server whose whole point is a small resource budget. A hidden tab learns
// nothing from them, so the timer skips while the page is hidden and fires once
// as soon as it comes back.

/** Like setInterval, but the callback is skipped while the page is hidden. */
export function pollInterval(fn: () => void, ms: number): () => void {
  let last = Date.now();
  const id = setInterval(() => {
    if (document.hidden) return;
    last = Date.now();
    fn();
  }, ms);
  // Coming back to a stale tab should show current numbers immediately.
  const onVisible = () => {
    if (!document.hidden && Date.now() - last >= ms) {
      last = Date.now();
      fn();
    }
  };
  document.addEventListener("visibilitychange", onVisible);
  return () => {
    clearInterval(id);
    document.removeEventListener("visibilitychange", onVisible);
  };
}
