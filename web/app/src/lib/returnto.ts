import { api } from "@/lib/api";

/**
 * Where forward-auth wanted this person to end up, and whether they can.
 *
 * A protected site sends its visitor to the panel with `?next=<the page they
 * asked for>`. Going back there is only useful if the browser will send the
 * sign-in cookie to that host, which depends on that cookie being scoped to a
 * parent domain both names share. Sending somebody to a host that will never
 * receive it starts the same redirect again, forever.
 *
 * So this answers one of three things: nothing to do, go here, or here is why
 * it cannot work — because landing on the dashboard with no explanation reads
 * as "the login failed" when the login worked and the two names simply cannot
 * share a cookie.
 */
export type ReturnTo =
  | { kind: "none" }
  | { kind: "go"; url: string; host: string }
  | { kind: "blocked"; host: string; why: string; cookieDomain: string; suggest: string };

export async function whereNext(search = location.search): Promise<ReturnTo> {
  const next = new URLSearchParams(search).get("next");
  if (!next) return { kind: "none" };
  let target: URL;
  try {
    target = new URL(next);
  } catch {
    return { kind: "none" };
  }
  if (target.protocol !== "https:" && target.protocol !== "http:") return { kind: "none" };

  let dom = "";
  try {
    dom = (await api.setupStatus()).cookieDomain ?? "";
  } catch {
    return { kind: "none" };
  }
  if (dom && (target.hostname === dom || target.hostname.endsWith("." + dom))) {
    return { kind: "go", url: target.toString(), host: target.hostname };
  }
  // The name both would have to share for a cookie to reach them. Not a
  // suggestion to apply blindly: widening the cookie means every host under
  // that parent can be handed the session, which is why the panel asks rather
  // than deciding.
  const suggest = sharedParent(location.hostname, target.hostname);
  if (!dom) {
    return {
      kind: "blocked",
      host: target.hostname,
      cookieDomain: "",
      suggest,
      why: `You are signed in, but ${target.hostname} has no way to see that. The cookie that proves it to a protected site is only issued once a session cookie domain is set, and none is, so a browser has nothing to send there and the site asks again.`,
    };
  }
  return {
    kind: "blocked",
    host: target.hostname,
    cookieDomain: dom,
    suggest: suggest && suggest !== dom ? suggest : "",
    why: `You are signed in, but ${target.hostname} is not under ${dom}, which is what the sign-in cookie is scoped to, so a browser will never send it there. An Islet login can only protect names under ${dom}.`,
  };
}

/**
 * The longest domain the panel and the protected host have in common, or "" if
 * they share nothing usable.
 *
 * "islet.example.com" and "admin.example.com" share "example.com". A public
 * suffix is not a parent anybody may claim: "a.co.uk" and "b.co.uk" share
 * nothing a cookie may be scoped to, and browsers refuse it anyway. Two labels
 * is the floor here, which is the common case and errs towards refusing.
 */
export function sharedParent(a: string, b: string): string {
  const x = a.toLowerCase().split("."), y = b.toLowerCase().split(".");
  const out: string[] = [];
  for (let i = 1; i <= Math.min(x.length, y.length); i++) {
    if (x[x.length - i] !== y[y.length - i]) break;
    out.unshift(x[x.length - i]);
  }
  if (out.length < 2) return "";
  const parent = out.join(".");
  // "a.co.uk" and "b.co.uk" share "co.uk", which nobody owns and no browser
  // will store a cookie for. The daemon refuses it too — this only avoids
  // offering it in the first place.
  if (out.length === 2 && out[1].length === 2 && PUBLIC_SECOND_LEVEL.has(out[0])) return "";
  return parent;
}

const PUBLIC_SECOND_LEVEL = new Set([
  "co", "com", "net", "org", "gov", "edu", "ac", "or", "ne", "me", "sch", "nhs", "ltd", "plc",
]);
