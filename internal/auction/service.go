// Package auction implements the AuctionService gRPC server.
// Each RunAuction call:
//  1. Checks budget availability for each candidate via the token-bucket pacer
//  2. Filters candidates below floor price
//  3. Ranks remaining candidates by eCPM (bid × CTR)
//  4. Runs second-price selection
//  5. Records the win and emits Prometheus metrics
package auction

import (
	"context"
	"time"

	pb "github.com/saitejasrivilli/ad-auction-engine/proto"
	"github.com/saitejasrivilli/ad-auction-engine/internal/metrics"
	"github.com/saitejasrivilli/ad-auction-engine/internal/pacing"
)

// Service implements pb.AuctionServiceServer.
type Service struct {
	pb.UnimplementedAuctionServiceServer
	pacer *pacing.BudgetPacer
}

// NewService creates an AuctionService backed by the given budget pacer.
func NewService(pacer *pacing.BudgetPacer) *Service {
	return &Service{pacer: pacer}
}

// RunAuction executes a full second-price auction for the given BidRequest.
func (s *Service) RunAuction(ctx context.Context, req *pb.BidRequest) (*pb.AuctionResult, error) {
	start := time.Now()
	metrics.AuctionRequestsTotal.Inc()

	result := &pb.AuctionResult{
		RequestId:    req.RequestId,
		CandidatesIn: int32(len(req.Candidates)),
	}

	// 1. Budget check — build cappedIDs set
	cappedIDs := make(map[string]bool)
	for _, c := range req.Candidates {
		throttled, err := s.pacer.TrySpend(ctx, c.AdvertiserId, 0) // probe only
		if err != nil || throttled {
			cappedIDs[c.AdvertiserId] = true
			metrics.AuctionBudgetThrottleTotal.Inc()
		}
	}

	// 2. Convert proto candidates to internal type
	candidates := make([]RankedCandidate, 0, len(req.Candidates))
	for _, c := range req.Candidates {
		candidates = append(candidates, RankedCandidate{
			AdID:         c.AdId,
			AdvertiserID: c.AdvertiserId,
			BidCPM:       c.BidCpm,
			PredictedCTR: c.PredictedCtr,
			DailyBudget:  c.DailyBudget,
		})
	}

	// 3. Rank (applies floor filter + eCPM sort)
	ranked, floorFiltered := RankCandidates(candidates, req.FloorPrice, cappedIDs)
	metrics.AuctionFloorFilterTotal.Add(float64(floorFiltered))
	result.FloorEnforced = floorFiltered > 0
	result.CandidatesOut = int32(len(ranked))

	// 4. Second-price selection
	auctionResult, ok := RunSecondPrice(ranked, req.FloorPrice)
	if !ok {
		metrics.AuctionNoFillTotal.Inc()
		result.LatencyUs = time.Since(start).Microseconds()
		return result, nil
	}

	winner := ranked[auctionResult.WinnerIdx]

	// 5. Deduct actual spend from budget
	_, err := s.pacer.TrySpend(ctx, winner.AdvertiserID, auctionResult.ClearingPriceCPM)
	if err != nil {
		// Non-fatal: log and continue — the auction has already resolved
		_ = err
	}

	// 6. Populate result
	result.WinnerAdId     = winner.AdID
	result.WinnerAdvId    = winner.AdvertiserID
	result.ClearingPrice  = auctionResult.ClearingPriceCPM
	result.Ecpm           = winner.eCPM
	result.BudgetCapped   = len(cappedIDs) > 0
	result.LatencyUs       = time.Since(start).Microseconds()

	// 7. Emit metrics
	metrics.AuctionFillsTotal.Inc()
	metrics.AuctionLatencyUS.Observe(float64(result.LatencyUs))
	metrics.AuctionClearingPriceCPM.Observe(auctionResult.ClearingPriceCPM)
	metrics.AuctionECPM.Observe(winner.eCPM)
	// RPM = clearing_price_cpm / 1000 * 1000 = clearing_price_cpm
	metrics.AuctionRevenuePerMille.Set(auctionResult.ClearingPriceCPM)

	return result, nil
}

// GetBudgetStatus returns the current token balance for an advertiser.
func (s *Service) GetBudgetStatus(ctx context.Context, req *pb.BudgetStatusRequest) (*pb.BudgetStatus, error) {
	remaining, err := s.pacer.Remaining(ctx, req.AdvertiserId)
	if err != nil {
		return nil, err
	}
	return &pb.BudgetStatus{
		AdvertiserId:   req.AdvertiserId,
		RemainingBudget: remaining,
		SpendRate:       s.pacer.SpendRate(req.AdvertiserId),
	}, nil
}
