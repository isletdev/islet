/**
 * Puts text on the clipboard, over plain HTTP as well.
 *
 * navigator.clipboard exists only in a secure context, and the panel opened
 * inside the VPN at http://10.66.0.1 is not one — WireGuard is the encryption
 * there, which the browser cannot know. The old route, a selected textarea and
 * execCommand("copy"), still works in every browser on a click, so it is the
 * fallback. Rejects when both are refused, so a caller can say so.
 */
export async function copyText(text: string): Promise<void> {
  if (window.isSecureContext && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return;
    } catch {
      // Refused: permissions, or a document without focus. Try the old way.
    }
  }
  const active = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  // Inside a modal dialog everything outside it is inert and cannot be
  // selected, so the textarea goes into the dialog.
  const host = active?.closest("dialog") ?? document.body;
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.setAttribute("readonly", "");
  ta.style.cssText = "position:fixed;top:0;left:0;width:1px;height:1px;opacity:0;";
  host.appendChild(ta);
  ta.select();
  let ok = false;
  try {
    ok = document.execCommand("copy");
  } finally {
    ta.remove();
    active?.focus();
  }
  if (!ok) throw new Error("the browser refused the clipboard");
}

/** A random hex id, where crypto.randomUUID is missing outside a secure context. */
export function randomId(bytes = 16): string {
  const b = new Uint8Array(bytes);
  crypto.getRandomValues(b);
  return Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
}
