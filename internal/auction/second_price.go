// Package auction implements second-price (Vickrey) auction selection.
// The winner pays the second-highest eCPM (or floor price if only one bidder).
package auction

// SecondPriceResult holds the auction outcome.
type SecondPriceResult struct {
	WinnerIdx      int
	ClearingPriceCPM float64 // price winner actually pays
}

// RunSecondPrice selects a winner from ranked candidates using Vickrey rules:
//   - Winner = highest eCPM candidate (index 0 after ranking)
//   - Clearing price = max(second_highest_bid, floor_price)
//
// Returns ok=false when the ranked slice is empty (no fill).
func RunSecondPrice(
	ranked []RankedCandidate,
	floorPrice float64,
) (result SecondPriceResult, ok bool) {
	if len(ranked) == 0 {
		return SecondPriceResult{}, false
	}

	winner := ranked[0]
	_ = winner

	var secondBid float64
	if len(ranked) >= 2 {
		secondBid = ranked[1].BidCPM
	} else {
		secondBid = floorPrice
	}

	clearingPrice := secondBid
	if floorPrice > clearingPrice {
		clearingPrice = floorPrice
	}

	return SecondPriceResult{
		WinnerIdx:        0,
		ClearingPriceCPM: clearingPrice,
	}, true
}
