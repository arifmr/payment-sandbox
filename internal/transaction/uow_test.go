package transaction

import (
	"context"
	"database/sql"
	"testing"
)

// FromCtx is what lets a repository run either standalone or inside a transaction.
// Getting it wrong means "atomic" operations silently execute outside the tx.

type fakeQuerier struct{ name string }

func (f *fakeQuerier) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, nil
}
func (f *fakeQuerier) QueryRowContext(context.Context, string, ...any) *sql.Row { return nil }
func (f *fakeQuerier) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, nil
}

var _ Querier = (*fakeQuerier)(nil)

func TestFromCtx_ReturnsFallbackWithoutTx(t *testing.T) {
	fallback := &fakeQuerier{name: "db"}

	if got := FromCtx(context.Background(), fallback); got != Querier(fallback) {
		t.Error("FromCtx must return the fallback when no tx is bound")
	}
}

func TestFromCtx_ReturnsBoundTx(t *testing.T) {
	fallback := &fakeQuerier{name: "db"}

	// A non-nil *sql.Tx value is enough: FromCtx only type-asserts, it never uses it.
	tx := &sql.Tx{}
	ctx := WithTx(context.Background(), tx)

	got := FromCtx(ctx, fallback)
	if got == Querier(fallback) {
		t.Fatal("FromCtx returned the fallback even though a tx was bound")
	}
	if got != Querier(tx) {
		t.Errorf("FromCtx returned %v, want the bound tx", got)
	}
}

// A nil tx stored in context must not be handed out — that would panic later.
func TestFromCtx_NilTxFallsBack(t *testing.T) {
	fallback := &fakeQuerier{name: "db"}
	ctx := WithTx(context.Background(), nil)

	if got := FromCtx(ctx, fallback); got != Querier(fallback) {
		t.Error("a nil tx in context must fall back to the plain connection")
	}
}

// The tx is keyed by a private type, so an unrelated context value of a different
// type cannot be mistaken for one.
func TestFromCtx_IgnoresUnrelatedContextValues(t *testing.T) {
	fallback := &fakeQuerier{name: "db"}

	type otherKey struct{}
	ctx := context.WithValue(context.Background(), otherKey{}, &sql.Tx{})

	if got := FromCtx(ctx, fallback); got != Querier(fallback) {
		t.Error("an unrelated context value must not be picked up as the tx")
	}
}

// Nesting must keep the innermost tx, matching how a sub-operation would rebind it.
func TestWithTx_InnermostWins(t *testing.T) {
	outer := &sql.Tx{}
	inner := &sql.Tx{}

	ctx := WithTx(WithTx(context.Background(), outer), inner)

	if got := FromCtx(ctx, &fakeQuerier{}); got != Querier(inner) {
		t.Error("the innermost tx must win")
	}
}

// WithTx must not mutate the parent context.
func TestWithTx_DoesNotAffectParent(t *testing.T) {
	fallback := &fakeQuerier{name: "db"}
	parent := context.Background()

	_ = WithTx(parent, &sql.Tx{})

	if got := FromCtx(parent, fallback); got != Querier(fallback) {
		t.Error("WithTx leaked the tx into the parent context")
	}
}
