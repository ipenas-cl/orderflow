#!/usr/bin/env bash
# Chaos scenarios against the docker-compose stack.
#   payments-decline  every charge is declined   -> saga releases stock, order CANCELLED
#   provider-flaky    60% 5xx + latency          -> retries/backoff absorb it, orders still DELIVERED
#   provider-outage   PSP fully down             -> retries exhausted, breaker opens, saga compensates
#   kill-payments     stop the payments service  -> commands wait durably in JetStream; restart drains them
#   undeliverable     carrier rejects address    -> refund + stock release after payment
#   reset             back to defaults
set -euo pipefail
PSP=${PSP_URL:-http://localhost:8090}
INV=${INVENTORY_URL:-http://localhost:8082}
here=$(dirname "$0")
chaos() { curl -fsS -X PUT "$PSP/admin/chaos" -H 'Content-Type: application/json' -d "$1" >/dev/null; echo "psp chaos: $1"; }
stock() { curl -fsS "$INV/v1/stock" | jq -r '.items[] | select(.sku=="SKU-KEYBOARD") | "stock SKU-KEYBOARD on_hand=\(.on_hand) reserved=\(.reserved)"'; }

case "${1:-}" in
  payments-decline) chaos '{"decline_rate":1}'; stock; "$here/place-order.sh" bob; stock; chaos '{"failure_rate":0.1,"decline_rate":0.05,"latency_ms":40,"jitter_ms":60}' ;;
  provider-flaky)   chaos '{"failure_rate":0.6,"latency_ms":150,"jitter_ms":300}'; "$here/place-order.sh" carol
                    curl -fsS localhost:8081/metrics | grep '^orderflow_provider_calls_total' || true
                    chaos '{"failure_rate":0.1,"decline_rate":0.05,"latency_ms":40,"jitter_ms":60}' ;;
  provider-outage)  chaos '{"outage":true}'; for c in d1 d2 d3; do "$here/place-order.sh" "$c" & done; wait
                    curl -fsS localhost:8081/metrics | grep -E '^orderflow_circuit_breaker_state' || true
                    chaos '{"failure_rate":0.1,"decline_rate":0.05,"latency_ms":40,"jitter_ms":60}' ;;
  kill-payments)    docker compose stop payments
                    ( "$here/place-order.sh" erin ) & sleep 5
                    echo "payments is down; the order waits at STOCK_RESERVED (command durable in JetStream). restarting..."
                    docker compose start payments; wait ;;
  undeliverable)    stock; "$here/place-order.sh" undeliverable-frank; stock ;;
  reset)            chaos '{"failure_rate":0.1,"decline_rate":0.05,"latency_ms":40,"jitter_ms":60}' ;;
  *) sed -n '2,10p' "$0"; exit 1 ;;
esac
