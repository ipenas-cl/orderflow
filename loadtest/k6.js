// k6 run loadtest/k6.js   (or: docker run --rm -i --network host grafana/k6 run - <loadtest/k6.js)
import http from "k6/http";
import { check, sleep } from "k6";

const ORDERS = __ENV.ORDERS_URL || "http://localhost:8080";
const PAYMENTS = __ENV.PAYMENTS_URL || "http://localhost:8081";
const SKUS = ["SKU-KEYBOARD", "SKU-MOUSE", "SKU-MONITOR"];

export const options = {
  scenarios: {
    orders: { executor: "ramping-arrival-rate", startRate: 5, timeUnit: "1s", preAllocatedVUs: 50,
      stages: [{ target: 30, duration: "30s" }, { target: 30, duration: "1m" }, { target: 0, duration: "15s" }] },
    idempotent_payments: { executor: "constant-vus", vus: 5, duration: "1m", exec: "payments" },
  },
  thresholds: {
    "http_req_failed{scenario:orders}": ["rate<0.01"],           // availability SLO
    "http_req_duration{scenario:orders}": ["p(99)<300"],         // latency SLO (accept path)
  },
};

export default function () {
  const body = JSON.stringify({ customer_id: `k6-${__VU}`, currency: "USD",
    lines: [{ sku: SKUS[__ITER % SKUS.length], qty: 1, unit_price_cents: 1999 }] });
  const r = http.post(`${ORDERS}/v1/orders`, body, { headers: { "Content-Type": "application/json" } });
  check(r, { "202 accepted": (x) => x.status === 202 || x.status === 429 });
}

// The same Idempotency-Key sent twice must return the same payment.
export function payments() {
  const key = `k6-${__VU}-${__ITER}`;
  const body = JSON.stringify({ order_id: key, amount_cents: 1000, currency: "USD" });
  const params = { headers: { "Content-Type": "application/json", "Idempotency-Key": key } };
  const a = http.post(`${PAYMENTS}/v1/payments`, body, params);
  const b = http.post(`${PAYMENTS}/v1/payments`, body, params);
  if (a.status === 201 && b.status === 201) {
    check(b, { "replayed same payment": (x) => x.json("id") === a.json("id") && x.headers["Idempotent-Replayed"] === "true" });
  }
  sleep(0.2);
}
