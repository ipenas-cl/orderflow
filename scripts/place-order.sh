#!/usr/bin/env bash
# Place one order and follow it through the saga.
# usage: scripts/place-order.sh [customer] [sku] [qty]
set -euo pipefail
ORDERS=${ORDERS_URL:-http://localhost:8080}
CUSTOMER=${1:-alice}; SKU=${2:-SKU-KEYBOARD}; QTY=${3:-1}
id=$(curl -fsS -X POST "$ORDERS/v1/orders" -H 'Content-Type: application/json' \
  -d "{\"customer_id\":\"$CUSTOMER\",\"currency\":\"USD\",\"lines\":[{\"sku\":\"$SKU\",\"qty\":$QTY,\"unit_price_cents\":4999}]}" | jq -r .id)
echo "order $id placed"
last=""
for _ in $(seq 1 60); do
  s=$(curl -fsS "$ORDERS/v1/orders/$id" | jq -r .status)
  [[ "$s" != "$last" ]] && echo "  -> $s" && last=$s
  [[ "$s" =~ ^(DELIVERED|CANCELLED|REFUNDED)$ ]] && break
  sleep 1
done
curl -fsS "$ORDERS/v1/orders/$id" | jq -r '.timeline[] | "  \(.at[11:19])  \(.step)  \(.detail // "")"'
