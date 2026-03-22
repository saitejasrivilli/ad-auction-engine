// Package auction implements eCPM-based ranking with floor price enforcement.
// eCPM = bid_cpm × predicted_ctr × 1000
// Candidates below floor_price are filtered before auction.
package auction

import "sort"

// RankedCandidate wraps a candidate with its computed eCPM.
type RankedCandidate struct {
	AdID          string
	AdvertiserID  string
	BidCPM        float64
	PredictedCTR  float64
	DailyBudget   float64
	eCPM          float64
}

// ComputeECPM returns bid_cpm * predicted_ctr.
// We omit the ×1000 factor here since it cancels in ranking;
// the clearing price is expressed in CPM units.
func ComputeECPM(bidCPM, predictedCTR float64) float64 {
	return bidCPM * predictedCTR
}

// RankCandidates filters by floor price, computes eCPM, and returns
// candidates sorted descending by eCPM.
// Budget-capped candidates (passed in via the cappedIDs set) are excluded.
func RankCandidates(
	candidates []RankedCandidate,
	floorPrice float64,
	cappedIDs map[string]bool,
) (ranked []RankedCandidate, floorFiltered int) {
	filtered := candidates[:0]

	for _, c := range candidates {
		if cappedIDs[c.AdvertiserID] {
			continue
		}
		if c.BidCPM < floorPrice {
			floorFiltered++
			continue
		}
		c.eCPM = ComputeECPM(c.BidCPM, c.PredictedCTR)
		filtered = append(filtered, c)
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].eCPM > filtered[j].eCPM
	})

	return filtered, floorFiltered
}
