import { useEffect, useState } from "react";
import { Api, money, orderTotal, type Order } from "../api";

const STATUSES = ["", "PENDING", "STOCK_RESERVED", "PAID", "SHIPPED", "DELIVERED", "CANCELLED", "REFUNDED"];

export function Orders({ api, open }: { api: Api; open: (id: string) => void }) {
  const [q, setQ] = useState("");
  const [status, setStatus] = useState("");
  const [orders, setOrders] = useState<Order[]>([]);
  const [err, setErr] = useState("");

  useEffect(() => {
    const load = () => api.orders(q, status).then((r) => setOrders(r.orders)).catch((e) => setErr(e.message));
    load();
    const t = setInterval(load, 3000);
    return () => clearInterval(t);
  }, [api, q, status]);

  return (
    <section>
      <h2>Orders</h2>
      <div className="toolbar">
        <input placeholder="Search order id or customer" value={q} onChange={(e) => setQ(e.target.value)} />
        <select value={status} onChange={(e) => setStatus(e.target.value)}>
          {STATUSES.map((s) => (
            <option key={s} value={s}>
              {s || "All statuses"}
            </option>
          ))}
        </select>
      </div>
      {err && <p className="error">{err}</p>}
      <table>
        <thead>
          <tr><th>Order</th><th>Customer</th><th>Total</th><th>Status</th><th>Created</th></tr>
        </thead>
        <tbody>
          {orders.map((o) => (
            <tr key={o.id} onClick={() => open(o.id)} className="clickable">
              <td className="mono">{o.id.slice(0, 8)}</td>
              <td>{o.customer_id}</td>
              <td>{money(orderTotal(o), o.currency)}</td>
              <td><span className={`status ${o.status}`}>{o.status}</span></td>
              <td>{new Date(o.created_at).toLocaleString()}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}
