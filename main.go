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
	"fmt"
	"log"
	"net"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"github.com/saitejasrivilli/ad-auction-engine/internal/auction"
	"github.com/saitejasrivilli/ad-auction-engine/internal/pacing"
	pb "github.com/saitejasrivilli/ad-auction-engine/proto"
)

func main() {
	grpcPort   := envOr("AUCTION_GRPC_PORT", "50051")
	metricsPort := envOr("AUCTION_METRICS_PORT", "9090")
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

	// ── Prometheus metrics ───────────────────────────────────────────────────
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "ok")
		})
		log.Printf("[info] Prometheus metrics on :%s/metrics", metricsPort)
		if err := http.ListenAndServe(":"+metricsPort, mux); err != nil {
			log.Fatalf("metrics server: %v", err)
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
