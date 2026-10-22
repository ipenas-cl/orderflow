// Package contracts is the published language shared by the services:
// message subjects and payloads that travel over the broker.
package contracts

const (
	Stream = "ORDERFLOW"

	SubjInventoryReserve  = "orderflow.inventory.cmd.reserve"
	SubjInventoryRelease  = "orderflow.inventory.cmd.release"
	SubjInventoryReserved = "orderflow.inventory.evt.reserved"
	SubjInventoryRejected = "orderflow.inventory.evt.rejected"
	SubjInventoryReleased = "orderflow.inventory.evt.released"

	SubjPaymentAuthorize  = "orderflow.payments.cmd.authorize"
	SubjPaymentAuthorized = "orderflow.payments.evt.authorized"
	SubjPaymentFailed     = "orderflow.payments.evt.failed"
	SubjPaymentRefunded   = "orderflow.payments.evt.refunded"

	SubjPaymentRefund = "orderflow.payments.cmd.refund"

	SubjFulfillmentCreate = "orderflow.fulfillment.cmd.create"
	SubjShipmentStatus    = "orderflow.fulfillment.evt.status"
	SubjFulfillmentFailed = "orderflow.fulfillment.evt.failed"

	SubjOrderConfirmed = "orderflow.orders.evt.confirmed"
	SubjOrderCancelled = "orderflow.orders.evt.cancelled"
)

type Line struct {
	SKU string `json:"sku"`
	Qty int    `json:"qty"`
}

type ReserveStock struct {
	OrderID string `json:"order_id"`
	Lines   []Line `json:"lines"`
}

type ReleaseStock struct {
	OrderID string `json:"order_id"`
	Reason  string `json:"reason"`
}

type StockReserved struct {
	OrderID string `json:"order_id"`
}

type StockRejected struct {
	OrderID string `json:"order_id"`
	Reason  string `json:"reason"`
}

type StockReleased struct {
	OrderID string `json:"order_id"`
}

type AuthorizePayment struct {
	OrderID     string `json:"order_id"`
	CustomerID  string `json:"customer_id"`
	AmountCents int64  `json:"amount_cents"`
	Currency    string `json:"currency"`
}

type PaymentAuthorized struct {
	OrderID     string `json:"order_id"`
	PaymentID   string `json:"payment_id"`
	ProviderRef string `json:"provider_ref"`
}

type PaymentFailed struct {
	OrderID string `json:"order_id"`
	Reason  string `json:"reason"`
}

type PaymentRefunded struct {
	OrderID   string `json:"order_id"`
	PaymentID string `json:"payment_id"`
	Reason    string `json:"reason"`
}

type OrderConfirmed struct {
	OrderID string `json:"order_id"`
}

type OrderCancelled struct {
	OrderID string `json:"order_id"`
	Reason  string `json:"reason"`
}

type RefundPayment struct {
	OrderID   string `json:"order_id"`
	PaymentID string `json:"payment_id"`
	Reason    string `json:"reason"`
}

type CreateFulfillment struct {
	OrderID    string `json:"order_id"`
	CustomerID string `json:"customer_id"`
	Lines      []Line `json:"lines"`
}

// ShipmentStatus is a tracking event (PICKING, PACKED, SHIPPED, IN_TRANSIT, DELIVERED).
type ShipmentStatus struct {
	OrderID        string `json:"order_id"`
	ShipmentID     string `json:"shipment_id"`
	Status         string `json:"status"`
	Carrier        string `json:"carrier,omitempty"`
	TrackingNumber string `json:"tracking_number,omitempty"`
}

type FulfillmentFailed struct {
	OrderID string `json:"order_id"`
	Reason  string `json:"reason"`
}
