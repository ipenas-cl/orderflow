//go:build integration

// Integration tests for the Postgres adapters, the outbox and the inbox,
// against a real Postgres started with testcontainers.
package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/ipenas-cl/orderflow/internal/contracts"
	invpg "github.com/ipenas-cl/orderflow/internal/inventory/adapters/postgres"
	invapp "github.com/ipenas-cl/orderflow/internal/inventory/app"
	ordpg "github.com/ipenas-cl/orderflow/internal/orders/adapters/postgres"
	"github.com/ipenas-cl/orderflow/internal/orders/domain"
	"github.com/ipenas-cl/orderflow/internal/orders/ports"
	"github.com/ipenas-cl/orderflow/internal/platform/messaging"
	"github.com/ipenas-cl/orderflow/internal/platform/outbox"
	"github.com/ipenas-cl/orderflow/internal/platform/pg"
)

func db(t *testing.T, schema string) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	c, err := tcpg.Run(ctx, "postgres:16-alpine", tcpg.WithDatabase("test"), tcpg.WithUsername("u"), tcpg.WithPassword("p"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(90*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	url, _ := c.ConnectionString(ctx, "sslmode=disable")
	pool, err := pg.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pg.Migrate(ctx, pool, pg.PlatformSchema, schema); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestOrdersOutboxInboxAndOptimisticLocking(t *testing.T) {
	ctx := context.Background()
	pool := db(t, ordpg.Schema)
	uow := ordpg.UnitOfWork{Pool: pool}
	o, _ := domain.NewOrder("o-1", "c1", "USD", []domain.Line{{SKU: "A", Qty: 1, UnitPriceCents: 100}}, time.Now())

	err := uow.Do(ctx, func(ctx context.Context, tx ports.Tx) error {
		if err := tx.Orders.Create(ctx, o); err != nil {
			return err
		}
		env, _ := messaging.New(ctx, contracts.SubjInventoryReserve, o.ID, contracts.ReserveStock{OrderID: o.ID})
		return tx.Outbox.Add(ctx, env)
	})
	if err != nil {
		t.Fatal(err)
	}

	// Two stale copies: only one update may win.
	a, _ := uow.Orders().Get(ctx, o.ID)
	b, _ := uow.Orders().Get(ctx, o.ID)
	_ = a.MarkStockReserved(time.Now())
	_ = b.MarkStockReserved(time.Now())
	if err := uow.Orders().Update(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := uow.Orders().Update(ctx, b); err != ports.ErrConflict {
		t.Fatalf("want conflict, got %v", err)
	}

	// Inbox dedupe.
	for i, want := range []bool{true, false} {
		_ = uow.Do(ctx, func(ctx context.Context, tx ports.Tx) error {
			first, err := tx.Inbox.MarkProcessed(ctx, "c", "m1")
			if err != nil || first != want {
				t.Fatalf("call %d: first=%v err=%v", i, first, err)
			}
			return nil
		})
	}

	// Outbox drain publishes once and marks published.
	var mu sync.Mutex
	var got []outbox.Record
	pub := func(_ context.Context, r outbox.Record) error {
		mu.Lock()
		got = append(got, r)
		mu.Unlock()
		return nil
	}
	store := pg.OutboxStore{Pool: pool}
	if n, err := store.Drain(ctx, 10, pub); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if n, _ := store.Drain(ctx, 10, pub); n != 0 {
		t.Fatal("published twice")
	}
	if got[0].Subject != contracts.SubjInventoryReserve {
		t.Fatal(got)
	}
}

func TestInventoryConcurrentReservationsNeverOversell(t *testing.T) {
	ctx := context.Background()
	pool := db(t, invpg.Schema) // seeds SKU-LIMITED with 3 units
	svc := invapp.NewService(invpg.UnitOfWork{Pool: pool}, nil)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "order-" + string(rune('a'+i))
			env, _ := messaging.New(ctx, contracts.SubjInventoryReserve, id, contracts.ReserveStock{OrderID: id, Lines: []contracts.Line{{SKU: "SKU-LIMITED", Qty: 1}}})
			if err := svc.HandleMessage(ctx, env); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	var reserved, rejected int
	_ = pool.QueryRow(ctx, `SELECT reserved FROM items WHERE sku='SKU-LIMITED'`).Scan(&reserved)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE subject=$1`, contracts.SubjInventoryRejected).Scan(&rejected)
	if reserved != 3 || rejected != 7 {
		t.Fatalf("reserved=%d rejected=%d", reserved, rejected)
	}
}
