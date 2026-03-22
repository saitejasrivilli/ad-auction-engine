// Package benchmark measures auction throughput and latency.
// Run with: go test -bench=. -benchtime=10s -benchmem ./benchmark/
package benchmark

import (
	"context"
	"fmt"
	"math/rand"
	"testing"

	"github.com/saitejasrivilli/ad-auction-engine/internal/auction"
	"github.com/saitejasrivilli/ad-auction-engine/internal/pacing"
	pb "github.com/saitejasrivilli/ad-auction-engine/proto"
)

var (
	advIDs = []string{"adv_001","adv_002","adv_003","adv_004","adv_005"}
	svc    *auction.Service
)

func init() {
	store := pacing.NewInMemoryStore()
	pacer := pacing.NewBudgetPacer(store)

	ctx := context.Background()
	for _, id := range advIDs {
		store.SetMaxCap(id, 1000.0)
		_ = pacer.RegisterAdvertiser(ctx, id, 1000.0*86400)
	}
	svc = auction.NewService(pacer)
}

func makeBidRequest(n int) *pb.BidRequest {
	candidates := make([]*pb.Candidate, n)
	for i := range candidates {
		candidates[i] = &pb.Candidate{
			AdId:         fmt.Sprintf("ad_%04d", i),
			AdvertiserId: advIDs[i%len(advIDs)],
			BidCpm:       0.5 + rand.Float64()*4.5,
			PredictedCtr: 0.01 + rand.Float64()*0.09,
			DailyBudget:  500 + rand.Float64()*1000,
		}
	}
	return &pb.BidRequest{
		RequestId:   "bench_req",
		PlacementId: "placement_home",
		UserId:      "user_bench",
		FloorPrice:  0.1,
		Candidates:  candidates,
	}
}

// BenchmarkRunAuction_10candidates simulates a typical 10-candidate auction.
func BenchmarkRunAuction_10candidates(b *testing.B) {
	req := makeBidRequest(10)
	ctx := context.Background()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = svc.RunAuction(ctx, req)
		}
	})
}

// BenchmarkRunAuction_50candidates simulates a denser auction.
func BenchmarkRunAuction_50candidates(b *testing.B) {
	req := makeBidRequest(50)
	ctx := context.Background()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = svc.RunAuction(ctx, req)
		}
	})
}

// BenchmarkRunAuction_100candidates stress test.
func BenchmarkRunAuction_100candidates(b *testing.B) {
	req := makeBidRequest(100)
	ctx := context.Background()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = svc.RunAuction(ctx, req)
		}
	})
}

// TestAuctionCorrectness verifies second-price selection.
func TestAuctionCorrectness(t *testing.T) {
	store := pacing.NewInMemoryStore()
	pacer := pacing.NewBudgetPacer(store)
	ctx := context.Background()

	for _, id := range advIDs {
		store.SetMaxCap(id, 1000.0)
		_ = pacer.RegisterAdvertiser(ctx, id, 1000.0*86400)
	}
	s := auction.NewService(pacer)

	req := &pb.BidRequest{
		RequestId:  "test_001",
		FloorPrice: 0.5,
		Candidates: []*pb.Candidate{
			{AdId:"ad_A", AdvertiserId:"adv_001", BidCpm:3.0, PredictedCtr:0.05, DailyBudget:500},
			{AdId:"ad_B", AdvertiserId:"adv_002", BidCpm:2.0, PredictedCtr:0.08, DailyBudget:500},
			{AdId:"ad_C", AdvertiserId:"adv_003", BidCpm:1.0, PredictedCtr:0.02, DailyBudget:500},
		},
	}

	result, err := s.RunAuction(ctx, req)
	if err != nil {
		t.Fatalf("RunAuction error: %v", err)
	}

	// ad_B has highest eCPM: 2.0 × 0.08 = 0.16 vs ad_A: 3.0 × 0.05 = 0.15
	if result.WinnerAdId != "ad_B" {
		t.Errorf("expected winner ad_B (eCPM=0.16), got %s (eCPM=%.4f)",
			result.WinnerAdId, result.Ecpm)
	}

	// Clearing price = second bid = ad_A's bid = 3.0
	if result.ClearingPrice != 3.0 {
		t.Errorf("expected clearing price 3.0, got %.4f", result.ClearingPrice)
	}

	t.Logf("winner=%s ecpm=%.4f clearing=%.4f latency=%dµs",
		result.WinnerAdId, result.Ecpm, result.ClearingPrice, result.LatencyUs)
}

// TestFloorPriceEnforcement verifies candidates below floor are excluded.
func TestFloorPriceEnforcement(t *testing.T) {
	store := pacing.NewInMemoryStore()
	pacer := pacing.NewBudgetPacer(store)
	ctx := context.Background()
	store.SetMaxCap("adv_001", 1000)
	_ = pacer.RegisterAdvertiser(ctx, "adv_001", 1000*86400)
	s := auction.NewService(pacer)

	req := &pb.BidRequest{
		RequestId:  "test_floor",
		FloorPrice: 2.0,
		Candidates: []*pb.Candidate{
			{AdId:"ad_below", AdvertiserId:"adv_001", BidCpm:1.0, PredictedCtr:0.5, DailyBudget:500},
		},
	}

	result, err := s.RunAuction(ctx, req)
	if err != nil {
		t.Fatalf("RunAuction error: %v", err)
	}
	if result.WinnerAdId != "" {
		t.Errorf("expected no winner (below floor), got %s", result.WinnerAdId)
	}
	if !result.FloorEnforced {
		t.Error("expected floor_enforced=true")
	}
}
