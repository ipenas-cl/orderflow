import { useEffect, useState } from "react";
import { Api, type Adjustment, type Item } from "../api";

export function Inventory({ api, can }: { api: Api; can: (p: string) => boolean }) {
  const [items, setItems] = useState<Item[]>([]);
  const [adj, setAdj] = useState<Adjustment[]>([]);
  const [form, setForm] = useState({ sku: "", delta: "", reason: "" });
  const [err, setErr] = useState("");
  const load = () =>
    api.inventory().then((r) => {
      setItems(r.stock.items);
      setAdj(r.adjustments.adjustments);
    }).catch((e) => setErr(e.message));

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const submit = async () => {
    try {
      setErr("");
      await api.adjust(form.sku, Number(form.delta), form.reason);
      setForm({ sku: "", delta: "", reason: "" });
      load();
    } catch (e) {
      setErr((e as Error).message);
    }
  };

  return (
    <section>
      <h2>Inventory</h2>
      <table>
        <thead><tr><th>SKU</th><th>Name</th><th>On hand</th><th>Reserved</th><th>Available</th></tr></thead>
        <tbody>
          {items.map((i) => (
            <tr key={i.sku}><td className="mono">{i.sku}</td><td>{i.name}</td><td>{i.on_hand}</td><td>{i.reserved}</td><td>{i.on_hand - i.reserved}</td></tr>
          ))}
        </tbody>
      </table>
      {can("inventory:adjust") && (
        <div className="toolbar">
          <input placeholder="SKU" value={form.sku} onChange={(e) => setForm({ ...form, sku: e.target.value })} />
          <input placeholder="+/- qty" value={form.delta} onChange={(e) => setForm({ ...form, delta: e.target.value })} />
          <input placeholder="Reason (receiving, cycle count…)" value={form.reason} onChange={(e) => setForm({ ...form, reason: e.target.value })} />
          <button disabled={!form.sku || !form.delta || !form.reason} onClick={submit}>Adjust stock</button>
        </div>
      )}
      {err && <p className="error">{err}</p>}
      <h3>Adjustments</h3>
      <table>
        <thead><tr><th>When</th><th>SKU</th><th>Delta</th><th>Reason</th><th>By</th></tr></thead>
        <tbody>
          {adj.map((a) => (
            <tr key={a.id}><td>{new Date(a.at).toLocaleString()}</td><td className="mono">{a.sku}</td><td>{a.delta > 0 ? `+${a.delta}` : a.delta}</td><td>{a.reason}</td><td>{a.actor}</td></tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}
