//go:build e2e

package e2e

import (
	"net/http"
	"sync"
	"testing"
)

// TestE2E_Topup_CreditsOnlyOnApproval is SRS section 2.2: a top-up arrives PENDING and the
// balance moves only when an admin approves it. Checking the balance between the two steps
// is what distinguishes "credits on approval" from "credits on request and happens to end
// up right".
func TestE2E_Topup_CreditsOnlyOnApproval(t *testing.T) {
	m := newMerchant(t)

	tu := m.topup(t, 250_000)
	if got := m.balance(t); got != 0 {
		t.Fatalf("balance = %d while the top-up is still PENDING, want 0", got)
	}

	requireStatus(t, settleTopup(t, tu.ID, "SUCCESS"), http.StatusOK)
	if got := m.balance(t); got != 250_000 {
		t.Errorf("balance = %d after approval, want 250000", got)
	}
}

// TestE2E_Topup_RejectedTopupNeverCredits covers the FAILED branch.
func TestE2E_Topup_RejectedTopupNeverCredits(t *testing.T) {
	m := newMerchant(t)

	tu := m.topup(t, 90_000)
	requireStatus(t, settleTopup(t, tu.ID, "FAILED"), http.StatusOK)

	if got := m.balance(t); got != 0 {
		t.Errorf("balance = %d after a FAILED top-up, want 0", got)
	}

	// FAILED is terminal — it cannot be talked back into a credit.
	requireErrorCode(t, settleTopup(t, tu.ID, "SUCCESS"), http.StatusUnprocessableEntity, "INVALID_STATE")
	if got := m.balance(t); got != 0 {
		t.Errorf("balance = %d after a refused revival, want 0", got)
	}
}

// TestE2E_Topup_ConcurrentApprovalsCreditOnce is the same double-processing race as
// payments, on the other endpoint that creates money. Both paths need the guard, and a fix
// applied to only one of them is a plausible mistake — so both are tested.
func TestE2E_Topup_ConcurrentApprovalsCreditOnce(t *testing.T) {
	m := newMerchant(t)
	tu := m.topup(t, 75_000)

	const racers = 4
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		ok    int
		start = make(chan struct{})
	)

	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			resp := settleTopup(t, tu.ID, "SUCCESS")
			mu.Lock()
			if resp.Status == http.StatusOK {
				ok++
			}
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()

	if ok != 1 {
		t.Errorf("%d of %d concurrent approvals succeeded, want exactly 1", ok, racers)
	}
	if got := m.balance(t); got != 75_000 {
		t.Errorf("balance = %d after %d concurrent approvals, want 75000", got, racers)
	}
}

// TestE2E_Topup_AmountMustBePositive checks the guard on the endpoint that mints balance.
// A zero or negative top-up is either meaningless or a way to drain a wallet through the
// credit path.
func TestE2E_Topup_AmountMustBePositive(t *testing.T) {
	m := fundedMerchant(t, 50_000)

	for _, amount := range []int64{0, -1, -100_000} {
		t.Run("", func(t *testing.T) {
			resp := m.post(t, "/api/v1/wallet/topup", map[string]any{"amount": amount})
			if resp.Status != http.StatusBadRequest {
				t.Errorf("amount %d: status = %d, want 400\nbody: %s", amount, resp.Status, resp.Body)
			}
		})
	}

	if got := m.balance(t); got != 50_000 {
		t.Errorf("balance = %d after rejected top-ups, want 50000", got)
	}
}

// TestE2E_Topup_MerchantSeesOnlyItsOwnTopups checks list scoping comes from the token.
func TestE2E_Topup_MerchantSeesOnlyItsOwnTopups(t *testing.T) {
	mine := newMerchant(t)
	theirs := newMerchant(t)

	myTopup := mine.topup(t, 11_000)
	theirTopup := theirs.topup(t, 22_000)

	r := mine.get(t, "/api/v1/wallet/topups?page=1&page_size=100")
	requireStatus(t, r, http.StatusOK)
	page := decode[paginated[topupResponse]](t, r)

	var sawMine bool
	for _, tu := range page.Data {
		if tu.ID == theirTopup.ID {
			t.Error("a merchant's top-up list contained another merchant's top-up")
		}
		if tu.ID == myTopup.ID {
			sawMine = true
		}
	}
	if !sawMine {
		t.Error("a merchant's top-up list did not contain its own top-up")
	}
}

// TestE2E_Wallet_BalanceIsScopedToTheCaller confirms two merchants cannot see each other's
// balance, and that the wallet returned belongs to the token holder.
func TestE2E_Wallet_BalanceIsScopedToTheCaller(t *testing.T) {
	rich := fundedMerchant(t, 500_000)
	poor := newMerchant(t)

	r := poor.get(t, "/api/v1/wallet")
	requireStatus(t, r, http.StatusOK)
	w := decode[walletResponse](t, r)

	if w.Balance != 0 {
		t.Errorf("balance = %d for a fresh merchant, want 0", w.Balance)
	}
	if w.MerchantID != poor.ID {
		t.Errorf("wallet merchant_id = %q, want the caller's id %q", w.MerchantID, poor.ID)
	}
	if w.MerchantID == rich.ID {
		t.Error("the wallet endpoint returned another merchant's wallet")
	}
}
