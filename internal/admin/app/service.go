// Package app holds the backoffice use cases. Authorization (RBAC) is checked
// here, in the application layer, so it holds for every driving adapter.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ipenas-cl/orderflow/internal/admin/ports"
	"github.com/ipenas-cl/orderflow/internal/platform/auth"
)

var ErrNotRefundable = errors.New("order is not refundable")

// Permissions maps each action to the roles allowed to perform it.
var Permissions = map[string][]string{
	"orders:read":       {auth.RoleViewer, auth.RoleSupport},
	"inventory:read":    {auth.RoleViewer, auth.RoleSupport},
	"shipments:read":    {auth.RoleViewer, auth.RoleSupport},
	"payments:refund":   {auth.RoleSupport},
	"shipments:advance": {auth.RoleSupport},
	"inventory:adjust":  {auth.RoleAdmin},
	"audit:read":        {auth.RoleAdmin},
}

type Service struct {
	backend ports.Backend
	audit   ports.AuditLog
	Now     func() time.Time
}

func NewService(b ports.Backend, a ports.AuditLog) *Service {
	return &Service{backend: b, audit: a, Now: func() time.Time { return time.Now().UTC() }}
}

func can(ctx context.Context, perm string) (auth.Principal, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return p, auth.ErrUnauthenticated
	}
	if !p.Has(Permissions[perm]...) {
		return p, fmt.Errorf("%w: %s", auth.ErrForbidden, perm)
	}
	return p, nil
}

func (s *Service) record(ctx context.Context, p auth.Principal, action, target, detail string, err error) {
	outcome := "success"
	if err != nil {
		outcome = "error: " + err.Error()
	}
	_ = s.audit.Append(ctx, ports.AuditEntry{At: s.Now(), Actor: actorName(p), Role: strings.Join(p.Roles, ","),
		Action: action, Target: target, Detail: detail, Outcome: outcome})
}

func actorName(p auth.Principal) string {
	if p.Email != "" {
		return p.Email
	}
	return p.Subject
}

func (s *Service) Me(ctx context.Context) (map[string]any, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return nil, auth.ErrUnauthenticated
	}
	perms := []string{}
	for k, roles := range Permissions {
		if p.Has(roles...) {
			perms = append(perms, k)
		}
	}
	return map[string]any{"principal": p, "permissions": perms}, nil
}

func (s *Service) ListOrders(ctx context.Context, status, q string) (json.RawMessage, error) {
	if _, err := can(ctx, "orders:read"); err != nil {
		return nil, err
	}
	return s.backend.ListOrders(ctx, status, q)
}

// OrderDetail composes order (with saga timeline), payments and shipment.
func (s *Service) OrderDetail(ctx context.Context, id string) (map[string]any, error) {
	if _, err := can(ctx, "orders:read"); err != nil {
		return nil, err
	}
	o, err := s.backend.GetOrder(ctx, id)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"order": o.Raw}
	if pays, err := s.backend.PaymentsForOrder(ctx, id); err == nil {
		out["payments"] = pays
	}
	if sh, err := s.backend.ShipmentForOrder(ctx, id); err == nil {
		out["shipment"] = sh
	}
	return out, nil
}

func (s *Service) Refund(ctx context.Context, orderID, reason string) (json.RawMessage, error) {
	p, err := can(ctx, "payments:refund")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(reason) == "" {
		return nil, fmt.Errorf("%w: reason required", ErrNotRefundable)
	}
	o, err := s.backend.GetOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	var res json.RawMessage
	switch o.Status {
	case "PAID", "SHIPPED", "DELIVERED":
		res, err = s.backend.Refund(ctx, o.PaymentID, "backoffice-refund-"+orderID, reason)
	default:
		err = fmt.Errorf("%w: status %s", ErrNotRefundable, o.Status)
	}
	s.record(ctx, p, "payments.refund", orderID, reason, err)
	return res, err
}

func (s *Service) Stock(ctx context.Context) (map[string]json.RawMessage, error) {
	if _, err := can(ctx, "inventory:read"); err != nil {
		return nil, err
	}
	st, err := s.backend.Stock(ctx)
	if err != nil {
		return nil, err
	}
	adj, err := s.backend.Adjustments(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]json.RawMessage{"stock": st, "adjustments": adj}, nil
}

func (s *Service) AdjustStock(ctx context.Context, sku string, delta int, reason string) (json.RawMessage, error) {
	p, err := can(ctx, "inventory:adjust")
	if err != nil {
		return nil, err
	}
	res, err := s.backend.AdjustStock(ctx, sku, delta, reason, actorName(p))
	s.record(ctx, p, "inventory.adjust", sku, fmt.Sprintf("delta=%+d reason=%s", delta, reason), err)
	return res, err
}

func (s *Service) Shipments(ctx context.Context, statuses string) (json.RawMessage, error) {
	if _, err := can(ctx, "shipments:read"); err != nil {
		return nil, err
	}
	return s.backend.Shipments(ctx, statuses)
}

func (s *Service) AdvanceShipment(ctx context.Context, id string) (json.RawMessage, error) {
	p, err := can(ctx, "shipments:advance")
	if err != nil {
		return nil, err
	}
	res, err := s.backend.AdvanceShipment(ctx, id, actorName(p))
	s.record(ctx, p, "shipments.advance", id, "", err)
	return res, err
}

func (s *Service) Audit(ctx context.Context) ([]ports.AuditEntry, error) {
	if _, err := can(ctx, "audit:read"); err != nil {
		return nil, err
	}
	return s.audit.List(ctx, 200)
}
