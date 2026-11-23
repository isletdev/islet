import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "@/lib/api";
import { pollInterval } from "@/lib/poll";

/**
 * Says, on every page, that the server's sites are switched off.
 *
 * Maintenance mode is a switch people forget: the work finishes, the panel
 * still works, and nothing in it says that every visitor is still reading the
 * maintenance page. So it is shown wherever the panel is open until somebody
 * turns it off.
 */
export default function MaintenanceNotice() {
  const [on, setOn] = useState(false);
  useEffect(() => {
    let alive = true;
    const check = () => api.maintenance().then((m) => { if (alive) setOn(m.on); }).catch(() => {});
    void check();
    const stop = pollInterval(() => void check(), 30000);
    window.addEventListener("islet:maintenance", check);
    return () => { alive = false; stop(); window.removeEventListener("islet:maintenance", check); };
  }, []);
  if (!on) return null;
  return (
    <div role="status" className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-warning/40 bg-warning-soft px-3 py-2 text-sm text-warning md:px-5">
      <span className="min-w-0 flex-1">Maintenance mode is on. Every site on this server shows the maintenance page; the panel still works.</span>
      <Link to="/domains" className="-my-1 py-1 font-medium underline">Turn it off</Link>
    </div>
  );
}
