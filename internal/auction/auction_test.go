package auction

import (
	"testing"
)

func fp(f float64) int64 { return int64(f * 100_000_000) }

func TestEquilibriumBasicCross(t *testing.T) {
	bids := []LevelInfo{
		{Price: fp(100), Quantity: 500},
		{Price: fp(99), Quantity: 300},
	}
	asks := []LevelInfo{
		{Price: fp(99), Quantity: 400},
		{Price: fp(100), Quantity: 200},
	}

	price, vol := CalculateEquilibrium(bids, asks, 0, 0, fp(99.5))
	if price == 0 {
		t.Fatal("expected non-zero equilibrium price")
	}
	if vol == 0 {
		t.Fatal("expected non-zero matched volume")
	}
}

func TestEquilibriumNoCrossing(t *testing.T) {
	bids := []LevelInfo{
		{Price: fp(90), Quantity: 100},
	}
	asks := []LevelInfo{
		{Price: fp(100), Quantity: 100},
	}

	price, vol := CalculateEquilibrium(bids, asks, 0, 0, fp(95))
	if vol != 0 {
		t.Errorf("expected 0 volume with no crossing, got %d at price %d", vol, price)
	}
}

func TestEquilibriumEmptyBook(t *testing.T) {
	price, vol := CalculateEquilibrium(nil, nil, 0, 0, 0)
	if price != 0 || vol != 0 {
		t.Errorf("empty book: price=%d vol=%d", price, vol)
	}
}

func TestEquilibriumMarketOrders(t *testing.T) {
	bids := []LevelInfo{
		{Price: fp(100), Quantity: 500},
	}
	asks := []LevelInfo{
		{Price: fp(100), Quantity: 300},
	}

	// Market buy adds 200 to bid side
	price, vol := CalculateEquilibrium(bids, asks, 200, 0, fp(100))
	if price != fp(100) {
		t.Errorf("price = %d, want %d", price, fp(100))
	}
	// Matched should be min(500+200, 300) = 300
	if vol != 300 {
		t.Errorf("vol = %d, want 300", vol)
	}
}

func TestEquilibriumTiebreakByReference(t *testing.T) {
	// Two prices with same matched volume; pick closest to reference
	bids := []LevelInfo{
		{Price: fp(102), Quantity: 100},
		{Price: fp(101), Quantity: 100},
	}
	asks := []LevelInfo{
		{Price: fp(101), Quantity: 100},
		{Price: fp(102), Quantity: 100},
	}

	// Both 101 and 102 yield matched volume of 100
	// Reference is 101 → should pick 101
	price, _ := CalculateEquilibrium(bids, asks, 0, 0, fp(101))
	if price != fp(101) {
		t.Errorf("expected tiebreak to pick %d (closest to ref), got %d", fp(101), price)
	}
}

func TestEquilibriumMaximizesVolume(t *testing.T) {
	bids := []LevelInfo{
		{Price: fp(103), Quantity: 100},
		{Price: fp(102), Quantity: 200},
		{Price: fp(101), Quantity: 300},
	}
	asks := []LevelInfo{
		{Price: fp(101), Quantity: 100},
		{Price: fp(102), Quantity: 200},
		{Price: fp(103), Quantity: 400},
	}

	// At 101: bid vol = 100+200+300=600, ask vol = 100 → matched = 100
	// At 102: bid vol = 100+200=300, ask vol = 100+200=300 → matched = 300
	// At 103: bid vol = 100, ask vol = 100+200+400=700 → matched = 100
	// Best is 102 with matched=300
	price, vol := CalculateEquilibrium(bids, asks, 0, 0, fp(102))
	if price != fp(102) {
		t.Errorf("price = %d, want %d", price, fp(102))
	}
	if vol != 300 {
		t.Errorf("vol = %d, want 300", vol)
	}
}

func TestEquilibriumSingleLevel(t *testing.T) {
	bids := []LevelInfo{{Price: fp(50), Quantity: 100}}
	asks := []LevelInfo{{Price: fp(50), Quantity: 200}}

	price, vol := CalculateEquilibrium(bids, asks, 0, 0, fp(50))
	if price != fp(50) {
		t.Errorf("price = %d", price)
	}
	if vol != 100 {
		t.Errorf("vol = %d, want 100", vol)
	}
}

func TestEquilibriumOnlyMarketOrders(t *testing.T) {
	// No limit orders, only market orders — no candidate prices
	price, vol := CalculateEquilibrium(nil, nil, 100, 100, fp(50))
	if vol != 0 {
		t.Errorf("market-only: vol = %d (no price levels to match at)", vol)
	}
	_ = price
}

func TestSortPrices(t *testing.T) {
	prices := []int64{50, 20, 80, 10, 30}
	sortPrices(prices)
	for i := 1; i < len(prices); i++ {
		if prices[i] < prices[i-1] {
			t.Errorf("not sorted: %v", prices)
			break
		}
	}
}
