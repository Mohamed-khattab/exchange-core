// Package auction implements equilibrium price calculation and auction uncross logic.
package auction

import (
	"math"
)

// PriceVolume represents cumulative volume available at a price level.
type PriceVolume struct {
	Price  int64
	BidVol uint64 // cumulative buy volume at or above this price
	AskVol uint64 // cumulative sell volume at or below this price
}

// CalculateEquilibrium finds the auction equilibrium price that maximizes matched volume.
// bids: price levels with quantities (descending price order)
// asks: price levels with quantities (ascending price order)
// marketBuyQty/marketSellQty: total market order quantities
// referencePrice: last traded price for tiebreaking
// Returns the equilibrium price and matched volume. Returns (0, 0) if no crossing exists.
func CalculateEquilibrium(
	bids []LevelInfo,
	asks []LevelInfo,
	marketBuyQty, marketSellQty uint64,
	referencePrice int64,
) (int64, uint64) {
	if len(bids) == 0 && len(asks) == 0 {
		return 0, 0
	}

	// Collect all candidate prices from both sides
	priceSet := make(map[int64]bool)
	for _, b := range bids {
		priceSet[b.Price] = true
	}
	for _, a := range asks {
		priceSet[a.Price] = true
	}
	if len(priceSet) == 0 {
		return 0, 0
	}

	// Sort candidate prices ascending
	prices := make([]int64, 0, len(priceSet))
	for p := range priceSet {
		prices = append(prices, p)
	}
	sortPrices(prices)

	// Compute cumulative bid volume (at or above each price) — walk bids descending
	// bids are already in descending price order
	cumBid := make(map[int64]uint64)
	var runningBid uint64 = marketBuyQty
	bidIdx := 0
	for i := len(prices) - 1; i >= 0; i-- {
		p := prices[i]
		for bidIdx < len(bids) && bids[bidIdx].Price >= p {
			runningBid += bids[bidIdx].Quantity
			bidIdx++
		}
		cumBid[p] = runningBid
	}

	// Compute cumulative ask volume (at or below each price) — walk asks ascending
	cumAsk := make(map[int64]uint64)
	var runningAsk uint64 = marketSellQty
	askIdx := 0
	for i := 0; i < len(prices); i++ {
		p := prices[i]
		for askIdx < len(asks) && asks[askIdx].Price <= p {
			runningAsk += asks[askIdx].Quantity
			askIdx++
		}
		cumAsk[p] = runningAsk
	}

	// Find price that maximizes matchedVolume = min(cumBid, cumAsk)
	var bestPrice int64
	var bestVolume uint64
	bestDist := int64(math.MaxInt64)

	for _, p := range prices {
		bv := cumBid[p]
		av := cumAsk[p]
		matched := bv
		if av < matched {
			matched = av
		}
		if matched == 0 {
			continue
		}

		dist := p - referencePrice
		if dist < 0 {
			dist = -dist
		}

		// Select: max volume, then closest to reference, then higher price
		if matched > bestVolume ||
			(matched == bestVolume && dist < bestDist) ||
			(matched == bestVolume && dist == bestDist && p > bestPrice) {
			bestPrice = p
			bestVolume = matched
			bestDist = dist
		}
	}

	return bestPrice, bestVolume
}

// LevelInfo provides price and quantity for a single level.
type LevelInfo struct {
	Price    int64
	Quantity uint64
}

func sortPrices(prices []int64) {
	// Simple insertion sort (candidate prices are typically small, <100)
	for i := 1; i < len(prices); i++ {
		key := prices[i]
		j := i - 1
		for j >= 0 && prices[j] > key {
			prices[j+1] = prices[j]
			j--
		}
		prices[j+1] = key
	}
}
