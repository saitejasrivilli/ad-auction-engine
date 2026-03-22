// Ad Auction Engine — main entry point
// Starts the gRPC server and Prometheus metrics HTTP endpoint.
//
// Environment variables:
//   AUCTION_GRPC_PORT    gRPC listen port          (default: 50051)
//   AUCTION_METRICS_PORT HTTP metrics port          (default: 9090)
//   REDIS_ADDR           Redis address              (default: localhost:6379)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"github.com/saitejasrivilli/ad-auction-engine/internal/auction"
	"github.com/saitejasrivilli/ad-auction-engine/internal/pacing"
	pb "github.com/saitejasrivilli/ad-auction-engine/proto"
)

func main() {
	grpcPort   := envOr("AUCTION_GRPC_PORT", "50051")
	metricsPort := envOr("PORT", envOr("AUCTION_METRICS_PORT", "9090"))
	redisAddr  := envOr("REDIS_ADDR", "localhost:6379")

	// ── Budget pacer ─────────────────────────────────────────────────────────
	var store pacing.BudgetStore

	redisStore, err := pacing.NewRedisStore(redisAddr)
	if err != nil {
		log.Printf("[warn] Redis unavailable (%v) — using in-memory store", err)
		store = pacing.NewInMemoryStore()
	} else {
		log.Printf("[info] Connected to Redis at %s", redisAddr)
		store = redisStore
	}

	pacer := pacing.NewBudgetPacer(store)
	defer pacer.Stop()

	// Seed demo advertisers
	ctx := context.Background()
	seedAdvertisers(ctx, pacer, redisStore)

	// ── gRPC server ──────────────────────────────────────────────────────────
	svc := auction.NewService(pacer)

	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(loggingInterceptor),
	)
	pb.RegisterAuctionServiceServer(grpcServer, svc)
	reflection.Register(grpcServer) // enables grpcurl introspection

	lis, err := net.Listen("tcp", ":"+grpcPort)
	if err != nil {
		log.Fatalf("failed to listen on :%s: %v", grpcPort, err)
	}

	// ── HTTP demo + metrics server ───────────────────────────────────────────
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "ok")
		})
		mux.HandleFunc("/", handleIndex)
		mux.HandleFunc("/auction", makeAuctionHandler(svc))
		mux.HandleFunc("/demo", makeDemoHandler(svc))
		log.Printf("[info] HTTP demo server on :%s", metricsPort)
		if err := http.ListenAndServe(":"+metricsPort, mux); err != nil {
			log.Fatalf("http server: %v", err)
		}
	}()

	log.Printf("[info] Auction gRPC server listening on :%s", grpcPort)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("grpc server: %v", err)
	}
}

// seedAdvertisers registers demo advertisers with daily budgets.
func seedAdvertisers(ctx context.Context, pacer *pacing.BudgetPacer, rs *pacing.RedisStore) {
	advertisers := map[string]float64{
		"adv_tech_001":    500.0,
		"adv_fashion_002": 300.0,
		"adv_travel_003":  800.0,
		"adv_food_004":    150.0,
		"adv_gaming_005":  1200.0,
	}
	for id, budget := range advertisers {
		if rs != nil {
			rs.SetMaxCap(id, budget/86400.0)
		}
		if err := pacer.RegisterAdvertiser(ctx, id, budget); err != nil {
			log.Printf("[warn] register %s: %v", id, err)
		}
	}
	log.Printf("[info] Seeded %d demo advertisers", len(advertisers))
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func loggingInterceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	res, err := handler(ctx, req)
	if err != nil {
		log.Printf("[grpc] %s error: %v", info.FullMethod, err)
	}
	return res, err
}

// ── HTTP handlers ─────────────────────────────────────────────────────────────

func handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head>
<title>Ad Auction Engine</title>
<style>
  body { font-family: system-ui, sans-serif; max-width: 860px; margin: 40px auto; padding: 0 20px; background: #0f0f0f; color: #e0e0e0; }
  h1   { color: #7c6fcd; margin-bottom: 4px; }
  h2   { color: #9d8fe0; margin-top: 32px; }
  p, li { color: #b0b0b0; line-height: 1.7; }
  code { background: #1e1e2e; padding: 2px 6px; border-radius: 4px; color: #cdd6f4; font-size: 0.9em; }
  pre  { background: #1e1e2e; padding: 16px; border-radius: 8px; overflow-x: auto; color: #cdd6f4; font-size: 0.88em; line-height: 1.6; }
  .badge { display: inline-block; background: #2a2a3e; border: 1px solid #7c6fcd; color: #a89ee0; padding: 3px 10px; border-radius: 12px; font-size: 0.8em; margin: 2px; }
  .endpoint { background: #1a1a2e; border-left: 3px solid #7c6fcd; padding: 12px 16px; border-radius: 0 8px 8px 0; margin: 12px 0; }
  a { color: #89b4fa; }
</style>
</head>
<body>
<h1>Ad Auction Engine</h1>
<p>Production-grade distributed ad auction service — second-price (Vickrey) auctions with eCPM ranking, smooth budget pacing, circuit breaking, and full observability.</p>

<div>
  <span class="badge">Go</span>
  <span class="badge">gRPC</span>
  <span class="badge">Redis</span>
  <span class="badge">Prometheus</span>
  <span class="badge">Docker</span>
  <span class="badge">581K QPS</span>
  <span class="badge">1720 ns/op</span>
</div>

<h2>How it works</h2>
<ol>
  <li><strong>Budget check</strong> — token-bucket pacer (Redis atomic DECR) throttles overspent advertisers</li>
  <li><strong>Floor filter</strong> — candidates below floor_price CPM are excluded</li>
  <li><strong>eCPM ranking</strong> — sort by <code>bid_cpm × predicted_ctr</code> descending</li>
  <li><strong>Second-price selection</strong> — winner pays max(second_bid, floor_price)</li>
  <li><strong>Metrics</strong> — fill rate, RPM, clearing price histogram → Prometheus</li>
</ol>

<h2>Live endpoints</h2>

<div class="endpoint">
  <code>POST /auction</code> — run a custom auction with your own candidates
</div>
<div class="endpoint">
  <code>GET /demo</code> — run a pre-built demo auction and see the full result
</div>
<div class="endpoint">
  <code>GET /metrics</code> — Prometheus metrics
</div>
<div class="endpoint">
  <code>GET /healthz</code> — health check
</div>

<h2>Try it — POST /auction</h2>
<pre>curl -X POST /auction \
  -H "Content-Type: application/json" \
  -d '{
    "request_id": "req_001",
    "floor_price": 0.5,
    "candidates": [
      {"ad_id":"ad_A","advertiser_id":"adv_tech_001","bid_cpm":3.0,"predicted_ctr":0.05,"daily_budget":500},
      {"ad_id":"ad_B","advertiser_id":"adv_fashion_002","bid_cpm":2.0,"predicted_ctr":0.08,"daily_budget":300},
      {"ad_id":"ad_C","advertiser_id":"adv_travel_003","bid_cpm":1.5,"predicted_ctr":0.04,"daily_budget":800}
    ]
  }'</pre>

<p>ad_B wins: <code>eCPM = 2.0 × 0.08 = 0.16</code> beats ad_A's <code>3.0 × 0.05 = 0.15</code>. Clearing price = ad_A's bid = <code>3.0 CPM</code>.</p>

<h2>Benchmark results</h2>
<pre>BenchmarkRunAuction_10candidates-8    6820303   1720 ns/op   968 B/op   5 allocs/op
BenchmarkRunAuction_50candidates-8    1509769   7914 ns/op  3720 B/op   5 allocs/op
BenchmarkRunAuction_100candidates-8    800780  15064 ns/op  6792 B/op   5 allocs/op

~581K QPS at 10 candidates · Apple M2 · 8 cores</pre>

<p><a href="https://github.com/saitejasrivilli/ad-auction-engine">GitHub →</a></p>
</body>
</html>`)
}

// makeAuctionHandler returns an HTTP handler that accepts a JSON BidRequest
// and returns a JSON AuctionResult.
func makeAuctionHandler(svc *auction.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "application/json")

		var reqBody struct {
			RequestID   string  `json:"request_id"`
			PlacementID string  `json:"placement_id"`
			UserID      string  `json:"user_id"`
			FloorPrice  float64 `json:"floor_price"`
			Candidates  []struct {
				AdID         string  `json:"ad_id"`
				AdvertiserID string  `json:"advertiser_id"`
				BidCPM       float64 `json:"bid_cpm"`
				PredictedCTR float64 `json:"predicted_ctr"`
				DailyBudget  float64 `json:"daily_budget"`
			} `json:"candidates"`
		}

		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
			return
		}

		pbCandidates := make([]*pb.Candidate, len(reqBody.Candidates))
		for i, c := range reqBody.Candidates {
			pbCandidates[i] = &pb.Candidate{
				AdId:         c.AdID,
				AdvertiserId: c.AdvertiserID,
				BidCpm:       c.BidCPM,
				PredictedCtr: c.PredictedCTR,
				DailyBudget:  c.DailyBudget,
			}
		}

		result, err := svc.RunAuction(r.Context(), &pb.BidRequest{
			RequestId:   reqBody.RequestID,
			PlacementId: reqBody.PlacementID,
			UserId:      reqBody.UserID,
			FloorPrice:  reqBody.FloorPrice,
			Candidates:  pbCandidates,
		})
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(result)
	}
}

// makeDemoHandler runs a pre-built demo auction so recruiters can see output instantly.
func makeDemoHandler(svc *auction.Service) http.HandlerFunc {
	advIDs := []string{"adv_tech_001", "adv_fashion_002", "adv_travel_003", "adv_food_004", "adv_gaming_005"}
	adNames := []string{"TechCorp Pro", "StyleHub", "TravelNow", "FoodieApp", "GameZone Ultra"}

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		candidates := make([]*pb.Candidate, 5)
		for i := range candidates {
			candidates[i] = &pb.Candidate{
				AdId:         adNames[i],
				AdvertiserId: advIDs[i],
				BidCpm:       0.5 + rng.Float64()*4.5,
				PredictedCtr: 0.01 + rng.Float64()*0.09,
				DailyBudget:  200 + rng.Float64()*1000,
			}
		}

		result, err := svc.RunAuction(r.Context(), &pb.BidRequest{
			RequestId:   fmt.Sprintf("demo_%d", time.Now().UnixMilli()),
			PlacementId: "home_feed",
			UserId:      "demo_user",
			FloorPrice:  0.3,
			Candidates:  candidates,
		})
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		// Enrich response with candidate details for readability
		type candidateDetail struct {
			AdID         string  `json:"ad_id"`
			AdvertiserID string  `json:"advertiser_id"`
			BidCPM       float64 `json:"bid_cpm"`
			PredictedCTR float64 `json:"predicted_ctr"`
			eCPM         float64 `json:"ecpm"`
		}
		details := make([]candidateDetail, len(candidates))
		for i, c := range candidates {
			details[i] = candidateDetail{
				AdID:         c.AdId,
				AdvertiserID: c.AdvertiserId,
				BidCPM:       round2(c.BidCpm),
				PredictedCTR: round2(c.PredictedCtr),
				eCPM:         round4(c.BidCpm * c.PredictedCtr),
			}
		}

		resp := map[string]interface{}{
			"auction_result": result,
			"all_candidates": details,
			"explanation":    "Winner = highest eCPM (bid × CTR). Clearing price = second-highest bid (Vickrey auction).",
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.Encode(resp)
	}
}

func round2(f float64) float64 { return float64(int(f*100)) / 100 }
func round4(f float64) float64 { return float64(int(f*10000)) / 10000 }
