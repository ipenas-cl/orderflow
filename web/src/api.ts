export type TimelineEntry = { at: string; step: string; detail?: string };
export type Order = {
  id: string;
  customer_id: string;
  currency: string;
  status: string;
  reason?: string;
  payment_id?: string;
  tracking_number?: string;
  lines: { sku: string; qty: number; unit_price_cents: number }[];
  timeline: TimelineEntry[];
  created_at: string;
};
export type Payment = { id: string; status: string; amount_cents: number; currency: string; provider: string; provider_ref?: string; reason?: string };
export type Shipment = {
  id: string;
  order_id: string;
  customer_id: string;
  status: string;
  carrier?: string;
  tracking_number?: string;
  failure_reason?: string;
  history: { at: string; status: string; actor: string }[];
};
export type Item = { sku: string; name: string; on_hand: number; reserved: number };
export type Adjustment = { id: number; sku: string; delta: number; reason: string; actor: string; at: string };
export type AuditEntry = { id: number; at: string; actor: string; role: string; action: string; target: string; detail: string; outcome: string };
export type Me = { principal: { sub: string; email?: string; roles: string[] }; permissions: string[] };

export class Api {
  constructor(private token: string) {}

  private async req<T>(method: string, path: string, body?: unknown): Promise<T> {
    const r = await fetch(path, {
      method,
      headers: { Authorization: `Bearer ${this.token}`, "Content-Type": "application/json" },
      body: body ? JSON.stringify(body) : undefined,
    });
    const data = await r.json().catch(() => ({}));
    if (!r.ok) throw new Error(data.error ?? `HTTP ${r.status}`);
    return data as T;
  }

  me = () => this.req<Me>("GET", "/api/me");
  orders = (q: string, status: string) =>
    this.req<{ orders: Order[] }>("GET", `/api/orders?${new URLSearchParams({ q, status })}`);
  order = (id: string) =>
    this.req<{ order: Order; payments?: { payments: Payment[] }; shipment?: { shipments: Shipment[] } }>("GET", `/api/orders/${id}`);
  refund = (id: string, reason: string) => this.req<Payment>("POST", `/api/orders/${id}/refund`, { reason });
  inventory = () => this.req<{ stock: { items: Item[] }; adjustments: { adjustments: Adjustment[] } }>("GET", "/api/inventory");
  adjust = (sku: string, delta: number, reason: string) => this.req<Item>("POST", "/api/inventory/adjustments", { sku, delta, reason });
  shipments = (status: string) => this.req<{ shipments: Shipment[] }>("GET", `/api/shipments?status=${encodeURIComponent(status)}`);
  advance = (id: string) => this.req<Shipment>("POST", `/api/shipments/${id}/advance`);
  audit = () => this.req<AuditEntry[]>("GET", "/api/audit");
}

export const money = (cents: number, cur: string) =>
  new Intl.NumberFormat("en-US", { style: "currency", currency: cur }).format(cents / 100);

export const orderTotal = (o: Order) => o.lines.reduce((s, l) => s + l.qty * l.unit_price_cents, 0);

/** Steps of the saga in display order; used to render the progress bar. */
export const SAGA_STEPS = ["PENDING", "STOCK_RESERVED", "PAID", "SHIPPED", "DELIVERED"] as const;

export function sagaProgress(status: string): number {
  const i = SAGA_STEPS.indexOf(status as (typeof SAGA_STEPS)[number]);
  return i < 0 ? -1 : i;
}
