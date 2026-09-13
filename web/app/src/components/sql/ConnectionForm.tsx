import { useState, type FormEvent } from "react";
import { sql, SqlError, type Connection, type ConnectionForm as Form, type Engine, type Environment } from "@/lib/sql";
import { useDialog, failure } from "@/lib/dialogs";
import { Button, Field, Input, Select } from "@/components/ui";

/**
 * Adding or editing an external database.
 *
 * It tests before it saves, so a connection that cannot work never becomes a
 * row somebody has to debug later. The password is write-only: it goes in and
 * never comes back, not even masked, so editing a connection and leaving the
 * field blank keeps the password that is already stored.
 */

const DEFAULT_PORT: Record<Engine, number> = { postgres: 5432, mysql: 3306, mariadb: 3306 };

interface Props {
  connection: Connection | null;
  onClose: () => void;
  onSaved: () => void | Promise<void>;
  onDeleted: (ref: string) => void | Promise<void>;
}

export default function ConnectionForm({ connection, onClose, onSaved, onDeleted }: Props) {
  const ask = useDialog();
  const editing = connection !== null;
  const [f, setF] = useState<Form>(() => ({
    name: connection?.name ?? "",
    engine: connection?.engine ?? "postgres",
    host: connection?.host ?? "",
    port: connection?.port ?? 5432,
    username: "",
    password: "",
    database: connection?.database ?? "",
    tls: "disable",
    readOnly: connection?.readOnly ?? false,
    environment: connection?.environment ?? "development",
  }));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const set = <K extends keyof Form>(k: K, v: Form[K]) => setF((x) => ({ ...x, [k]: v }));

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const body: Form = {
        ...f,
        // Blank on an edit means "keep the one you have"; null says so.
        password: editing && f.password === "" ? null : f.password,
      };
      if (editing && connection) await sql.updateConnection(connection.ref, body);
      else await sql.addConnection(body);
      await onSaved();
    } catch (err) {
      setError(err instanceof SqlError ? err.message : failure(err));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    if (!connection) return;
    const ok = await ask.confirm({
      title: `Forget ${connection.name}?`,
      body: "The database is not touched. This panel stops listing it and the stored password is deleted.",
      confirmLabel: "Forget it",
      tone: "danger",
    });
    if (!ok) return;
    try {
      await sql.deleteConnection(connection.ref);
      await onDeleted(connection.ref);
    } catch (err) {
      void ask.alert({ title: "Could not remove it", body: failure(err), tone: "danger" });
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" onClick={onClose}>
      <form
        onSubmit={submit}
        onClick={(e) => e.stopPropagation()}
        className="w-full max-w-xl overflow-hidden rounded-lg border border-border bg-surface shadow-float"
      >
        <div className="border-b border-border px-4 py-3">
          <h2 className="text-sm font-semibold">{editing ? `Edit ${connection?.name}` : "Add a database"}</h2>
          <p className="mt-0.5 text-xs text-ink-muted">
            A database on another server, or one on this server that Islet did not install. The connection is
            tested before it is saved.
          </p>
        </div>

        <div className="space-y-3 p-4">
          <div className="flex flex-wrap items-start gap-3">
            <Field label="Name" className="w-44">
              <Input value={f.name} onChange={(e) => set("name", e.target.value)} placeholder="reporting" required />
            </Field>
            <Field label="Engine" className="w-36">
              <Select
                value={f.engine}
                onChange={(e) => {
                  const engine = e.target.value as Engine;
                  setF((x) => ({ ...x, engine, port: DEFAULT_PORT[engine] }));
                }}
              >
                <option value="postgres">PostgreSQL</option>
                <option value="mysql">MySQL</option>
                <option value="mariadb">MariaDB</option>
              </Select>
            </Field>
            <Field label="Host" className="w-52" hint="An address this server can reach.">
              <Input value={f.host} onChange={(e) => set("host", e.target.value)} placeholder="10.0.0.5" className="font-mono" required />
            </Field>
            <Field label="Port" className="w-24">
              <Input
                value={String(f.port)}
                onChange={(e) => set("port", Number(e.target.value) || 0)}
                inputMode="numeric"
                className="font-mono"
                required
              />
            </Field>
          </div>

          <div className="flex flex-wrap items-start gap-3">
            <Field label="User" className="w-44">
              <Input value={f.username} onChange={(e) => set("username", e.target.value)} className="font-mono" required={!editing} />
            </Field>
            <Field
              label="Password"
              className="w-52"
              hint={editing ? "Leave blank to keep the stored one." : undefined}
            >
              <Input
                type="password"
                value={f.password ?? ""}
                onChange={(e) => set("password", e.target.value)}
                autoComplete="off"
              />
            </Field>
            <Field label="Database" className="w-44">
              <Input value={f.database} onChange={(e) => set("database", e.target.value)} className="font-mono" />
            </Field>
            <Field label="TLS" className="w-36">
              <Select value={f.tls} onChange={(e) => set("tls", e.target.value as Form["tls"])}>
                <option value="disable">Off</option>
                <option value="require">Required</option>
                <option value="verify-full">Required, verified</option>
              </Select>
            </Field>
          </div>

          <div className="flex flex-wrap items-start gap-3">
            <Field label="Environment" className="w-44" hint="Production makes every write ask first.">
              <Select value={f.environment} onChange={(e) => set("environment", e.target.value as Environment)}>
                <option value="development">Development</option>
                <option value="staging">Staging</option>
                <option value="production">Production</option>
              </Select>
            </Field>
            <label className="flex items-center gap-2 pt-6 text-xs">
              <input
                type="checkbox"
                checked={f.readOnly}
                onChange={(e) => set("readOnly", e.target.checked)}
                className="h-3.5 w-3.5 accent-[var(--islet-accent)]"
              />
              Read-only — the daemon refuses anything that is not a read
            </label>
          </div>

          {error && <p className="rounded-md bg-danger-soft px-2 py-1.5 text-xs text-danger">{error}</p>}
        </div>

        <div className="flex items-center gap-2 border-t border-border px-4 py-3">
          <Button type="submit" disabled={busy}>{busy ? "Testing…" : editing ? "Test and save" : "Test and add"}</Button>
          <Button type="button" variant="secondary" onClick={onClose}>Cancel</Button>
          {editing && (
            <Button type="button" variant="danger" className="ml-auto" onClick={() => void remove()}>Forget</Button>
          )}
        </div>
      </form>
    </div>
  );
}
