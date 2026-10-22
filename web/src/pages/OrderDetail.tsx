import { useCallback, useEffect, useState } from "react";
import { Api, money, orderTotal, SAGA_STEPS, sagaProgress, type Order, type Payment, type Shipment } from "../api";

export function OrderDetail({ api, id, can, back }: { api: Api; id: string; can: (p: string) => boolean; back: () => void }) {
  const [order, setOrder] = useState<Order>();
  const [payments, setPayments] = useState<Payment[]>([]);
  const [shipment, setShipment] = useState<Shipment>();
  const [reason, setReason] = useState("");
  const [msg, setMsg] = useState("");

  const load = useCallback(async () => {
    const d = await api.order(id);
    setOrder(d.order);
    setPayments(d.payments?.payments ?? []);
    setShipment(d.shipment?.shipments?.[0]);
  }, [api, id]);

  useEffect(() => {
    load();
    const t = setInterval(load, 2000);
    return () => clearInterval(t);
  }, [load]);

  if (!order) return <p>Loading…</p>;
  const progress = sagaProgress(order.status);
  const refundable = ["PAID", "SHIPPED", "DELIVERED"].includes(order.status);

  const refund = async () => {
    try {
      await api.refund(order.id, reason);
      setMsg("Refund issued. The order will move to REFUNDED when the event arrives.");
      setReason("");
      load();
    } catch (e) {
      setMsg(String((e as Error).message));
    }
  };

  return (
    <section>
      <button className="link" onClick={back}>← back to orders</button>
      <h2>
        Order <span className="mono">{order.id}</span> <span className={`status ${order.status}`}>{order.status}</span>
      </h2>
      <p className="muted">
        {order.customer_id} · {money(orderTotal(order), order.currency)} {order.reason && `· ${order.reason}`}
      </p>

      <ol className="saga">
        {SAGA_STEPS.map((s, i) => (
          <li key={s} className={progress >= i ? "done" : progress < 0 ? "aborted" : ""}>{s.replace("_", " ")}</li>
        ))}
      </ol>

      <div className="grid">
        <div className="card">
          <h3>Saga timeline</h3>
          <ul className="timeline">
            {order.timeline.map((t, i) => (
              <li key={i} className={t.step.startsWith("compensation") || t.step === "cancelled" ? "comp" : ""}>
                <time>{new Date(t.at).toLocaleTimeString()}</time> <b>{t.step}</b> {t.detail && <span className="muted">{t.detail}</span>}
              </li>
            ))}
          </ul>
        </div>
        <div className="card">
          <h3>Payments</h3>
          {payments.map((p) => (
            <p key={p.id}>
              <span className={`status ${p.status}`}>{p.status}</span> {money(p.amount_cents, p.currency)} via {p.provider}{" "}
              <span className="mono">{p.provider_ref}</span> {p.reason && <span className="muted">· {p.reason}</span>}
            </p>
          ))}
          {can("payments:refund") && refundable && (
            <div className="toolbar">
              <input placeholder="Refund reason (required)" value={reason} onChange={(e) => setReason(e.target.value)} />
              <button disabled={!reason} onClick={refund}>Issue refund</button>
            </div>
          )}
          {msg && <p className="muted">{msg}</p>}
          <h3>Shipment</h3>
          {shipment ? (
            <>
              <p>
                <span className={`status ${shipment.status}`}>{shipment.status}</span> {shipment.carrier} <span className="mono">{shipment.tracking_number}</span>
              </p>
              {shipment.failure_reason && <p className="error">{shipment.failure_reason}</p>}
            </>
          ) : (
            <p className="muted">Not created yet</p>
          )}
        </div>
      </div>
    </section>
  );
}
