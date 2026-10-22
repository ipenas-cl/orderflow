package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ipenas-cl/orderflow/internal/admin/adapters/memory"
	"github.com/ipenas-cl/orderflow/internal/admin/app"
	"github.com/ipenas-cl/orderflow/internal/admin/ports"
	"github.com/ipenas-cl/orderflow/internal/platform/auth"
)

type fakeBackend struct {
	ports.Backend
	status  string
	refunds int
}

func (f *fakeBackend) GetOrder(context.Context, string) (ports.Order, error) {
	return ports.Order{ID: "o1", Status: f.status, PaymentID: "p1"}, nil
}
func (f *fakeBackend) Refund(context.Context, string, string, string) (json.RawMessage, error) {
	f.refunds++
	return json.RawMessage(`{}`), nil
}
func (f *fakeBackend) AdjustStock(context.Context, string, int, string, string) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func as(role string) context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{Subject: "u", Email: role + "@x", Roles: []string{role}})
}

func TestRefundRBACAndAudit(t *testing.T) {
	fb := &fakeBackend{status: "PAID"}
	audit := &memory.Audit{}
	svc := app.NewService(fb, audit)
	if _, err := svc.Refund(as(auth.RoleViewer), "o1", "x"); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("viewer refunded")
	}
	if _, err := svc.Refund(as(auth.RoleSupport), "o1", "damaged"); err != nil || fb.refunds != 1 {
		t.Fatal(err)
	}
	fb.status = "CANCELLED"
	if _, err := svc.Refund(as(auth.RoleSupport), "o1", "again"); !errors.Is(err, app.ErrNotRefundable) {
		t.Fatal(err)
	}
	if len(audit.E) != 2 || audit.E[0].Actor != "support@x" || audit.E[1].Outcome == "success" {
		t.Fatalf("%+v", audit.E)
	}
}

func TestAdjustRequiresAdminAndAuditReadable(t *testing.T) {
	svc := app.NewService(&fakeBackend{}, &memory.Audit{})
	if _, err := svc.AdjustStock(as(auth.RoleSupport), "A", 1, "x"); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("support adjusted stock")
	}
	if _, err := svc.AdjustStock(as(auth.RoleAdmin), "A", 1, "receiving"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Audit(as(auth.RoleSupport)); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("support read audit")
	}
	if e, _ := svc.Audit(as(auth.RoleAdmin)); len(e) != 1 {
		t.Fatal(e)
	}
}
