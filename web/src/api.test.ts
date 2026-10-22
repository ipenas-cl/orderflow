import { describe, expect, it } from "vitest";
import { money, orderTotal, sagaProgress, type Order } from "./api";

describe("helpers", () => {
  it("formats money", () => expect(money(123456, "USD")).toBe("$1,234.56"));
  it("totals lines", () => {
    const o = { lines: [{ sku: "a", qty: 2, unit_price_cents: 500 }, { sku: "b", qty: 1, unit_price_cents: 250 }] } as Order;
    expect(orderTotal(o)).toBe(1250);
  });
  it("maps saga progress", () => {
    expect(sagaProgress("PAID")).toBe(2);
    expect(sagaProgress("CANCELLED")).toBe(-1);
  });
});
