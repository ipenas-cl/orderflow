import { useEffect, useState } from "react";
import { Api, type AuditEntry } from "../api";

export function Audit({ api }: { api: Api }) {
  const [rows, setRows] = useState<AuditEntry[]>([]);
  const [err, setErr] = useState("");
  useEffect(() => {
    api.audit().then(setRows).catch((e) => setErr(e.message));
  }, [api]);
  return (
    <section>
      <h2>Audit log</h2>
      {err && <p className="error">{err}</p>}
      <table>
        <thead><tr><th>When</th><th>Actor</th><th>Role</th><th>Action</th><th>Target</th><th>Detail</th><th>Outcome</th></tr></thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.id}>
              <td>{new Date(r.at).toLocaleString()}</td><td>{r.actor}</td><td>{r.role}</td><td className="mono">{r.action}</td>
              <td className="mono">{r.target.slice(0, 12)}</td><td>{r.detail}</td><td className={r.outcome === "success" ? "" : "error"}>{r.outcome}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}
