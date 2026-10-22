import { useEffect, useState } from "react";
import { Api, type Shipment } from "../api";

const NEXT: Record<string, string> = { PENDING: "Start picking", PICKING: "Mark packed", PACKED: "Book carrier & ship", SHIPPED: "Poll tracking", IN_TRANSIT: "Poll tracking" };

export function Fulfillment({ api, can, open }: { api: Api; can: (p: string) => boolean; open: (id: string) => void }) {
  const [filter, setFilter] = useState("PENDING,PICKING,PACKED,SHIPPED,IN_TRANSIT");
  const [rows, setRows] = useState<Shipment[]>([]);
  const [err, setErr] = useState("");
  const load = () => api.shipments(filter).then((r) => setRows(r.shipments)).catch((e) => setErr(e.message));

  useEffect(() => {
    load();
    const t = setInterval(load, 2000);
    return () => clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filter]);

  return (
    <section>
      <h2>Fulfillment queue</h2>
      <div className="toolbar">
        <select value={filter} onChange={(e) => setFilter(e.target.value)}>
          <option value="PENDING,PICKING,PACKED,SHIPPED,IN_TRANSIT">Open</option>
          <option value="DELIVERED">Delivered</option>
          <option value="FAILED">Failed</option>
          <option value="">All</option>
        </select>
      </div>
      {err && <p className="error">{err}</p>}
      <table>
        <thead>
          <tr><th>Order</th><th>Customer</th><th>Status</th><th>Carrier / tracking</th><th>Last update</th><th /></tr>
        </thead>
        <tbody>
          {rows.map((s) => (
            <tr key={s.id}>
              <td className="mono clickable" onClick={() => open(s.order_id)}>{s.order_id.slice(0, 8)}</td>
              <td>{s.customer_id}</td>
              <td><span className={`status ${s.status}`}>{s.status}</span></td>
              <td>{s.carrier} <span className="mono">{s.tracking_number}</span>{s.failure_reason && <span className="error"> {s.failure_reason}</span>}</td>
              <td>{s.history.at(-1) && `${new Date(s.history.at(-1)!.at).toLocaleTimeString()} by ${s.history.at(-1)!.actor}`}</td>
              <td>
                {can("shipments:advance") && NEXT[s.status] && (
                  <button onClick={() => api.advance(s.id).then(load).catch((e) => setErr(e.message))}>{NEXT[s.status]}</button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}
