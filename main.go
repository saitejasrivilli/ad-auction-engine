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
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Ad Auction Engine</title>
<link href="https://fonts.googleapis.com/css2?family=Share+Tech+Mono&family=Exo+2:wght@300;400;600;700&display=swap" rel="stylesheet">
<style>
:root {
  --navy:    #020b18;
  --navy2:   #041428;
  --navy3:   #071e34;
  --cyan:    #00e5ff;
  --cyan2:   #00bcd4;
  --cyan3:   #0097a7;
  --gold:    #ffd600;
  --red:     #ff1744;
  --green:   #00e676;
  --text:    #cce8f4;
  --muted:   #5a8fa8;
  --border:  rgba(0,229,255,0.15);
  --glow:    0 0 20px rgba(0,229,255,0.3);
}
*{box-sizing:border-box;margin:0;padding:0}
html{scroll-behavior:smooth}
body{
  font-family:'Exo 2',sans-serif;
  background:var(--navy);
  color:var(--text);
  min-height:100vh;
  overflow-x:hidden;
}

/* ── starfield ── */
#stars{position:fixed;top:0;left:0;width:100%;height:100%;pointer-events:none;z-index:0}
.star{position:absolute;background:#fff;border-radius:50%;animation:twinkle var(--d,3s) ease-in-out infinite var(--delay,0s)}
@keyframes twinkle{0%,100%{opacity:0.1}50%{opacity:0.8}}

/* ── scan line ── */
body::after{
  content:'';position:fixed;top:0;left:0;width:100%;height:2px;
  background:linear-gradient(90deg,transparent,var(--cyan),transparent);
  animation:scan 4s linear infinite;z-index:999;pointer-events:none;
  box-shadow:0 0 8px var(--cyan);
}
@keyframes scan{0%{top:-2px}100%{top:100vh}}

/* ── layout ── */
.wrap{position:relative;z-index:1;max-width:1100px;margin:0 auto;padding:0 24px 80px}

/* ── hero ── */
.hero{
  text-align:center;padding:80px 0 60px;
  border-bottom:1px solid var(--border);
  position:relative;
}
.hero::before{
  content:'';position:absolute;bottom:0;left:50%;transform:translateX(-50%);
  width:60%;height:1px;background:linear-gradient(90deg,transparent,var(--cyan),transparent);
}
.hero-eyebrow{
  font-family:'Share Tech Mono',monospace;
  font-size:0.75rem;letter-spacing:0.25em;color:var(--cyan2);
  margin-bottom:16px;animation:fadeIn 0.6s ease both;
}
.hero h1{
  font-size:clamp(2.2rem,5vw,3.8rem);font-weight:700;line-height:1.1;
  color:#fff;margin-bottom:12px;
  text-shadow:0 0 40px rgba(0,229,255,0.4);
  animation:fadeIn 0.8s ease both 0.1s;
}
.hero h1 span{color:var(--cyan)}
.hero-sub{
  font-size:1rem;color:var(--muted);max-width:600px;margin:0 auto 32px;
  font-weight:300;line-height:1.7;animation:fadeIn 0.8s ease both 0.2s;
}
.badges{display:flex;flex-wrap:wrap;justify-content:center;gap:8px;animation:fadeIn 0.8s ease both 0.3s}
.badge{
  font-family:'Share Tech Mono',monospace;font-size:0.72rem;
  padding:4px 12px;border:1px solid var(--cyan3);color:var(--cyan2);
  background:rgba(0,229,255,0.05);border-radius:2px;
  transition:all 0.2s;cursor:default;
}
.badge:hover{background:rgba(0,229,255,0.12);border-color:var(--cyan);color:var(--cyan);box-shadow:var(--glow)}
.badge.gold{border-color:var(--gold);color:var(--gold);background:rgba(255,214,0,0.05)}
.badge.gold:hover{background:rgba(255,214,0,0.12);box-shadow:0 0 20px rgba(255,214,0,0.3)}

@keyframes fadeIn{from{opacity:0;transform:translateY(16px)}to{opacity:1;transform:none}}

/* ── section headers ── */
.section{margin:60px 0 0}
.section-label{
  font-family:'Share Tech Mono',monospace;
  font-size:0.7rem;letter-spacing:0.2em;color:var(--cyan3);
  margin-bottom:6px;text-transform:uppercase;
}
.section h2{
  font-size:1.4rem;font-weight:600;color:#fff;
  margin-bottom:24px;padding-bottom:10px;
  border-bottom:1px solid var(--border);
  display:flex;align-items:center;gap:10px;
}
.section h2::before{content:'//';color:var(--cyan);font-family:'Share Tech Mono',monospace;font-size:1rem}

/* ── metrics grid ── */
.metrics{display:grid;grid-template-columns:repeat(auto-fit,minmax(160px,1fr));gap:16px;margin-bottom:32px}
.metric-card{
  background:var(--navy2);border:1px solid var(--border);
  padding:20px;border-radius:4px;text-align:center;
  position:relative;overflow:hidden;
  transition:border-color 0.3s,box-shadow 0.3s;
}
.metric-card::before{
  content:'';position:absolute;top:0;left:0;right:0;height:2px;
  background:linear-gradient(90deg,transparent,var(--cyan),transparent);
  opacity:0;transition:opacity 0.3s;
}
.metric-card:hover{border-color:var(--cyan);box-shadow:var(--glow)}
.metric-card:hover::before{opacity:1}
.metric-val{
  font-family:'Share Tech Mono',monospace;
  font-size:1.6rem;font-weight:400;color:var(--cyan);
  display:block;margin-bottom:4px;
}
.metric-val.gold{color:var(--gold)}
.metric-val.green{color:var(--green)}
.metric-lbl{font-size:0.72rem;color:var(--muted);letter-spacing:0.05em;text-transform:uppercase}

/* ── auction visualizer ── */
#visualizer{
  background:var(--navy2);border:1px solid var(--border);
  border-radius:4px;padding:28px;margin-bottom:0;position:relative;overflow:hidden;
}
#visualizer::before{
  content:'LIVE AUCTION SIMULATION';
  font-family:'Share Tech Mono',monospace;font-size:0.65rem;letter-spacing:0.2em;
  color:var(--cyan3);position:absolute;top:12px;right:16px;
}
.vis-controls{display:flex;gap:12px;margin-bottom:24px;flex-wrap:wrap;align-items:center}
.btn{
  font-family:'Share Tech Mono',monospace;font-size:0.78rem;
  padding:8px 20px;border:1px solid var(--cyan);color:var(--cyan);
  background:transparent;cursor:pointer;border-radius:2px;
  transition:all 0.2s;letter-spacing:0.05em;
}
.btn:hover{background:rgba(0,229,255,0.1);box-shadow:var(--glow)}
.btn.running{border-color:var(--gold);color:var(--gold);animation:pulse 1s ease-in-out infinite}
@keyframes pulse{0%,100%{box-shadow:0 0 0 rgba(255,214,0,0)}50%{box-shadow:0 0 16px rgba(255,214,0,0.4)}}
.btn-stop{border-color:var(--red);color:var(--red)}
.btn-stop:hover{background:rgba(255,23,68,0.1)}

.candidate-grid{display:flex;flex-direction:column;gap:10px}
.candidate-row{
  display:grid;grid-template-columns:120px 1fr 70px 70px 90px 80px;
  gap:12px;align-items:center;
  background:var(--navy3);border:1px solid var(--border);
  padding:12px 16px;border-radius:3px;
  transition:all 0.4s;position:relative;overflow:hidden;
}
.candidate-row.winner{
  border-color:var(--gold);
  background:rgba(255,214,0,0.05);
  box-shadow:0 0 20px rgba(255,214,0,0.15);
}
.candidate-row.winner::before{
  content:'WINNER';
  font-family:'Share Tech Mono',monospace;font-size:0.6rem;letter-spacing:0.15em;
  color:var(--gold);position:absolute;top:4px;right:8px;
}
.candidate-row.eliminated{opacity:0.3;filter:grayscale(0.8)}
.candidate-row.new-entry{animation:slideIn 0.35s ease both}
@keyframes slideIn{from{opacity:0;transform:translateX(-12px)}to{opacity:1;transform:none}}

.c-name{font-family:'Share Tech Mono',monospace;font-size:0.78rem;color:var(--text);white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.c-bar-wrap{position:relative;height:6px;background:rgba(0,229,255,0.08);border-radius:2px;overflow:hidden}
.c-bar{height:100%;border-radius:2px;transition:width 0.6s cubic-bezier(0.4,0,0.2,1);background:var(--cyan)}
.c-bar.winner-bar{background:var(--gold)}
.c-val{font-family:'Share Tech Mono',monospace;font-size:0.75rem;text-align:right}
.c-bid{color:var(--muted)}
.c-ctr{color:var(--cyan2)}
.c-ecpm{color:var(--text);font-weight:600}
.c-status{font-family:'Share Tech Mono',monospace;font-size:0.65rem;text-align:center;letter-spacing:0.05em}
.c-status.ok{color:var(--green)}
.c-status.throttled{color:var(--red)}
.c-status.floor{color:var(--muted)}

.col-headers{
  display:grid;grid-template-columns:120px 1fr 70px 70px 90px 80px;
  gap:12px;padding:0 16px 8px;
  font-family:'Share Tech Mono',monospace;font-size:0.62rem;
  color:var(--muted);letter-spacing:0.08em;text-transform:uppercase;
}

#result-panel{
  margin-top:20px;padding:16px 20px;
  background:var(--navy);border:1px solid var(--border);border-radius:3px;
  font-family:'Share Tech Mono',monospace;font-size:0.82rem;
  min-height:60px;transition:all 0.4s;
}
#result-panel.has-result{border-color:var(--gold);background:rgba(255,214,0,0.03)}
.result-row{display:flex;gap:32px;flex-wrap:wrap}
.result-field{display:flex;flex-direction:column;gap:2px}
.result-key{font-size:0.6rem;color:var(--muted);letter-spacing:0.1em;text-transform:uppercase}
.result-val{color:var(--cyan);font-size:0.9rem}
.result-val.gold{color:var(--gold)}
.result-val.green{color:var(--green)}

#auction-log{
  margin-top:12px;height:80px;overflow-y:auto;
  font-family:'Share Tech Mono',monospace;font-size:0.7rem;
  color:var(--muted);padding:8px 12px;
  background:rgba(0,0,0,0.3);border-radius:2px;
}
#auction-log .log-entry{margin-bottom:2px;line-height:1.5}
#auction-log .log-entry.highlight{color:var(--cyan)}
#auction-log .log-entry.win{color:var(--gold)}

/* ── pipeline diagram ── */
.pipeline{display:flex;align-items:center;gap:0;margin:8px 0;overflow-x:auto;padding:4px 0}
.pipe-step{
  flex-shrink:0;background:var(--navy3);border:1px solid var(--border);
  padding:10px 16px;border-radius:3px;text-align:center;min-width:110px;
  transition:all 0.3s;
}
.pipe-step.active{border-color:var(--cyan);box-shadow:var(--glow);background:rgba(0,229,255,0.05)}
.pipe-step.done{border-color:var(--green);background:rgba(0,230,118,0.04)}
.pipe-label{font-family:'Share Tech Mono',monospace;font-size:0.65rem;color:var(--muted);letter-spacing:0.05em;margin-bottom:3px}
.pipe-val{font-size:0.8rem;color:var(--text);font-weight:600}
.pipe-arrow{color:var(--cyan3);font-size:1.1rem;padding:0 4px;flex-shrink:0}

/* ── code blocks ── */
.code-block{
  background:var(--navy2);border:1px solid var(--border);
  padding:20px;border-radius:4px;font-family:'Share Tech Mono',monospace;
  font-size:0.8rem;line-height:1.7;overflow-x:auto;color:var(--text);
  position:relative;
}
.code-block .comment{color:var(--muted)}
.code-block .key{color:var(--cyan2)}
.code-block .val{color:var(--gold)}
.code-block .str{color:var(--green)}

.copy-btn{
  position:absolute;top:12px;right:12px;
  font-family:'Share Tech Mono',monospace;font-size:0.65rem;
  padding:4px 10px;border:1px solid var(--muted);color:var(--muted);
  background:transparent;cursor:pointer;border-radius:2px;
  transition:all 0.2s;
}
.copy-btn:hover{border-color:var(--cyan);color:var(--cyan)}

/* ── bench table ── */
.bench-table{width:100%;border-collapse:collapse;font-family:'Share Tech Mono',monospace;font-size:0.8rem}
.bench-table th{
  text-align:left;padding:8px 16px;
  color:var(--muted);font-size:0.65rem;letter-spacing:0.1em;text-transform:uppercase;
  border-bottom:1px solid var(--border);
}
.bench-table td{padding:10px 16px;border-bottom:1px solid rgba(0,229,255,0.05);color:var(--text)}
.bench-table tr:last-child td{border-bottom:none}
.bench-table tr:hover td{background:rgba(0,229,255,0.03)}
.bench-table .highlight-row td{color:var(--cyan)}
.bench-table .qps{color:var(--gold);font-weight:600}

/* ── footer ── */
footer{
  text-align:center;padding:32px 0;
  border-top:1px solid var(--border);margin-top:80px;
  font-family:'Share Tech Mono',monospace;font-size:0.72rem;color:var(--muted);
}
footer a{color:var(--cyan2);text-decoration:none}
footer a:hover{color:var(--cyan)}
</style>
</head>
<body>

<canvas id="stars"></canvas>

<div class="wrap">

  <!-- hero -->
  <div class="hero">
    <div class="hero-eyebrow">// DISTRIBUTED SYSTEMS · AD TECH · GO</div>
    <h1>Ad <span>Auction</span> Engine</h1>
    <p class="hero-sub">Production-grade second-price auction service with eCPM ranking, token-bucket budget pacing, circuit breaking, and full observability. Built in Go.</p>
    <div class="badges">
      <span class="badge">Go</span>
      <span class="badge">gRPC</span>
      <span class="badge">Redis</span>
      <span class="badge">Prometheus</span>
      <span class="badge">Docker</span>
      <span class="badge gold">581K QPS</span>
      <span class="badge gold">1720 ns/op</span>
      <span class="badge">second-price</span>
      <span class="badge">token-bucket pacing</span>
    </div>
  </div>

  <!-- metrics -->
  <div class="section">
    <div class="section-label">// performance</div>
    <h2>Benchmark results</h2>
    <div class="metrics">
      <div class="metric-card">
        <span class="metric-val gold">581K</span>
        <span class="metric-lbl">QPS (10 candidates)</span>
      </div>
      <div class="metric-card">
        <span class="metric-val">1720</span>
        <span class="metric-lbl">ns/op</span>
      </div>
      <div class="metric-card">
        <span class="metric-val green">5</span>
        <span class="metric-lbl">allocs/op</span>
      </div>
      <div class="metric-card">
        <span class="metric-val">8µs</span>
        <span class="metric-lbl">live latency</span>
      </div>
      <div class="metric-card">
        <span class="metric-val">50051</span>
        <span class="metric-lbl">gRPC port</span>
      </div>
      <div class="metric-card">
        <span class="metric-val green">2nd price</span>
        <span class="metric-lbl">auction type</span>
      </div>
    </div>
  </div>

  <!-- live visualizer -->
  <div class="section">
    <div class="section-label">// interactive</div>
    <h2>Live auction visualizer</h2>

    <div id="visualizer">
      <div class="vis-controls">
        <button class="btn" id="btn-run" onclick="startAuction()">▶ RUN AUCTION</button>
        <button class="btn btn-stop" id="btn-stop" onclick="stopAuction()" style="display:none">■ STOP</button>
        <button class="btn" onclick="runContinuous()" id="btn-cont">⟳ AUTO-RUN</button>
        <span style="font-family:'Share Tech Mono',monospace;font-size:0.7rem;color:var(--muted);margin-left:8px" id="auction-counter">auctions: 0</span>
      </div>

      <!-- pipeline steps -->
      <div class="pipeline" id="pipeline">
        <div class="pipe-step" id="p0"><div class="pipe-label">candidates</div><div class="pipe-val" id="pv0">—</div></div>
        <div class="pipe-arrow">→</div>
        <div class="pipe-step" id="p1"><div class="pipe-label">budget check</div><div class="pipe-val" id="pv1">—</div></div>
        <div class="pipe-arrow">→</div>
        <div class="pipe-step" id="p2"><div class="pipe-label">floor filter</div><div class="pipe-val" id="pv2">—</div></div>
        <div class="pipe-arrow">→</div>
        <div class="pipe-step" id="p3"><div class="pipe-label">ecpm rank</div><div class="pipe-val" id="pv3">—</div></div>
        <div class="pipe-arrow">→</div>
        <div class="pipe-step" id="p4"><div class="pipe-label">2nd price</div><div class="pipe-val" id="pv4">—</div></div>
        <div class="pipe-arrow">→</div>
        <div class="pipe-step" id="p5"><div class="pipe-label">winner</div><div class="pipe-val" id="pv5" style="color:var(--gold)">—</div></div>
      </div>

      <!-- candidate rows -->
      <div style="margin-top:20px">
        <div class="col-headers">
          <div>advertiser</div><div>ecpm score</div><div>bid cpm</div><div>ctr</div><div>ecpm</div><div>status</div>
        </div>
        <div class="candidate-grid" id="candidate-grid"></div>
      </div>

      <!-- result panel -->
      <div id="result-panel">
        <span style="color:var(--muted);font-family:'Share Tech Mono',monospace;font-size:0.75rem">// click RUN AUCTION to simulate a second-price auction</span>
      </div>

      <!-- log -->
      <div id="auction-log"></div>
    </div>
  </div>

  <!-- how it works -->
  <div class="section">
    <div class="section-label">// algorithm</div>
    <h2>How it works</h2>
    <div style="display:grid;grid-template-columns:repeat(auto-fit,minmax(240px,1fr));gap:16px">
      <div class="metric-card" style="text-align:left;padding:20px">
        <div style="font-family:'Share Tech Mono',monospace;font-size:0.65rem;color:var(--cyan3);margin-bottom:8px;letter-spacing:0.1em">STEP 01</div>
        <div style="font-size:0.95rem;font-weight:600;color:#fff;margin-bottom:6px">Budget check</div>
        <div style="font-size:0.82rem;color:var(--muted);line-height:1.6">Token-bucket pacer — Redis atomic Lua DECR. Throttled advertisers excluded before ranking.</div>
      </div>
      <div class="metric-card" style="text-align:left;padding:20px">
        <div style="font-family:'Share Tech Mono',monospace;font-size:0.65rem;color:var(--cyan3);margin-bottom:8px;letter-spacing:0.1em">STEP 02</div>
        <div style="font-size:0.95rem;font-weight:600;color:#fff;margin-bottom:6px">Floor price filter</div>
        <div style="font-size:0.82rem;color:var(--muted);line-height:1.6">Candidates with bid_cpm below floor_price are dropped. Floor enforced flag set in response.</div>
      </div>
      <div class="metric-card" style="text-align:left;padding:20px">
        <div style="font-family:'Share Tech Mono',monospace;font-size:0.65rem;color:var(--cyan3);margin-bottom:8px;letter-spacing:0.1em">STEP 03</div>
        <div style="font-size:0.95rem;font-weight:600;color:#fff;margin-bottom:6px">eCPM ranking</div>
        <div style="font-size:0.82rem;color:var(--muted);line-height:1.6">Sort by bid_cpm × predicted_ctr descending. Ranks by expected revenue, not raw bid.</div>
      </div>
      <div class="metric-card" style="text-align:left;padding:20px">
        <div style="font-family:'Share Tech Mono',monospace;font-size:0.65rem;color:var(--cyan3);margin-bottom:8px;letter-spacing:0.1em">STEP 04</div>
        <div style="font-size:0.95rem;font-weight:600;color:#fff;margin-bottom:6px">Second-price selection</div>
        <div style="font-size:0.82rem;color:var(--muted);line-height:1.6">Winner = highest eCPM. Clearing price = max(second_highest_bid, floor_price). Incentive-compatible.</div>
      </div>
    </div>
  </div>

  <!-- API -->
  <div class="section">
    <div class="section-label">// api</div>
    <h2>Try the API</h2>
    <div class="code-block">
      <button class="copy-btn" onclick="copyCode(this)">COPY</button>
<span class="comment"># POST /auction — run a custom auction</span>
curl -X POST https://ad-auction-engine.onrender.com/auction \
  -H <span class="str">"Content-Type: application/json"</span> \
  -d <span class="str">'{
    "request_id": "req_001",
    "floor_price": 0.5,
    "candidates": [
      {"ad_id":"ad_A","advertiser_id":"adv_001","bid_cpm":3.0,"predicted_ctr":0.05,"daily_budget":500},
      {"ad_id":"ad_B","advertiser_id":"adv_002","bid_cpm":2.0,"predicted_ctr":0.08,"daily_budget":300}
    ]
  }'</span>

<span class="comment"># ad_B wins: eCPM 0.16 (2.0×0.08) > ad_A eCPM 0.15 (3.0×0.05)</span>
<span class="comment"># clearing price = ad_A bid = 3.0 CPM  (Vickrey rule)</span>

<span class="comment"># GET /demo — randomized live auction</span>
curl https://ad-auction-engine.onrender.com/demo
    </div>
  </div>

  <!-- bench table -->
  <div class="section">
    <div class="section-label">// benchmarks</div>
    <h2>go test -bench=. -benchtime=10s</h2>
    <div class="code-block" style="padding:0;overflow:hidden">
      <table class="bench-table">
        <thead>
          <tr>
            <th>benchmark</th><th>iterations</th><th>ns/op</th><th>B/op</th><th>allocs/op</th><th>QPS (est.)</th>
          </tr>
        </thead>
        <tbody>
          <tr class="highlight-row">
            <td>10 candidates</td><td>6,820,303</td><td>1,720</td><td>968</td><td>5</td><td class="qps">~581K</td>
          </tr>
          <tr>
            <td>50 candidates</td><td>1,509,769</td><td>7,914</td><td>3,720</td><td>5</td><td class="qps">~126K</td>
          </tr>
          <tr>
            <td>100 candidates</td><td>800,780</td><td>15,064</td><td>6,792</td><td>5</td><td class="qps">~66K</td>
          </tr>
        </tbody>
      </table>
    </div>
    <p style="font-family:'Share Tech Mono',monospace;font-size:0.7rem;color:var(--muted);margin-top:8px">Apple M2 · 8 cores · go test -bench=. -benchtime=10s -benchmem</p>
  </div>

</div>

<footer>
  <p>Ad Auction Engine · <a href="https://github.com/saitejasrivilli/ad-auction-engine">github.com/saitejasrivilli/ad-auction-engine</a></p>
  <p style="margin-top:6px;font-size:0.65rem">Go · gRPC · Redis · Prometheus · Docker · second-price · token-bucket pacing</p>
</footer>

<script>
// ── starfield ────────────────────────────────────────────────────────────────
(function(){
  const c=document.getElementById('stars');
  const ctx=c.getContext('2d');
  c.width=window.innerWidth;c.height=window.innerHeight;
  const stars=Array.from({length:180},()=>({
    x:Math.random()*c.width,y:Math.random()*c.height,
    r:Math.random()*1.2+0.2,o:Math.random(),
    s:Math.random()*0.003+0.001,d:Math.random()>0.5?1:-1
  }));
  function draw(){
    ctx.clearRect(0,0,c.width,c.height);
    stars.forEach(s=>{
      s.o+=s.s*s.d;
      if(s.o>0.9||s.o<0.05)s.d*=-1;
      ctx.beginPath();ctx.arc(s.x,s.y,s.r,0,Math.PI*2);
      ctx.fillStyle='rgba(180,220,255,'+s.o+')';ctx.fill();
    });
    requestAnimationFrame(draw);
  }
  draw();
  window.addEventListener('resize',()=>{c.width=window.innerWidth;c.height=window.innerHeight});
})();

// ── auction visualizer ────────────────────────────────────────────────────────
const ADVERTISERS=[
  {id:'adv_tech_001',   name:'TechCorp'},
  {id:'adv_fashion_002',name:'StyleHub'},
  {id:'adv_travel_003', name:'TravelNow'},
  {id:'adv_food_004',   name:'FoodieApp'},
  {id:'adv_gaming_005', name:'GameZone'},
];

let auctionCount=0;
let autoInterval=null;
let running=false;

function rnd(min,max){return Math.random()*(max-min)+min}
function fmt2(n){return n.toFixed(2)}
function fmt4(n){return n.toFixed(4)}

function log(msg,cls=''){
  const el=document.getElementById('auction-log');
  const ts=new Date().toISOString().substr(11,12);
  const div=document.createElement('div');
  div.className='log-entry'+(cls?' '+cls:'');
  div.textContent='['+ts+'] '+msg;
  el.appendChild(div);
  el.scrollTop=el.scrollHeight;
  // keep max 40 lines
  while(el.children.length>40)el.removeChild(el.firstChild);
}

function setPipeStep(idx,val,state){
  for(let i=0;i<6;i++){
    document.getElementById('p'+i).className='pipe-step'+(i<idx?' done':'');
  }
  if(idx<6){
    document.getElementById('p'+idx).className='pipe-step active';
    document.getElementById('pv'+idx).textContent=val;
  }
}

function sleep(ms){return new Promise(r=>setTimeout(r,ms))}

async function runAuction(animate=true){
  if(running)return;
  running=true;

  const grid=document.getElementById('candidate-grid');
  const resultPanel=document.getElementById('result-panel');
  const floor=parseFloat((rnd(0.3,1.2)).toFixed(2));

  // generate candidates
  const candidates=ADVERTISERS.map(a=>({
    ...a,
    bid:parseFloat(rnd(0.5,5.0).toFixed(2)),
    ctr:parseFloat(rnd(0.01,0.10).toFixed(4)),
    budget:parseFloat(rnd(100,1200).toFixed(0)),
    throttled:Math.random()<0.15,
  })).map(c=>({...c,ecpm:parseFloat((c.bid*c.ctr).toFixed(4))}));

  auctionCount++;
  document.getElementById('auction-counter').textContent='auctions: '+auctionCount;
  log('--- auction #'+auctionCount+' | floor='+floor+' CPM ---','highlight');

  // step 0: show all candidates
  if(animate)setPipeStep(0,candidates.length,'active');
  grid.innerHTML='';
  candidates.forEach((c,i)=>{
    const row=document.createElement('div');
    row.className='candidate-row new-entry';
    row.id='row-'+i;
    row.innerHTML=
      '<div class="c-name">'+c.name+'</div>'+
      '<div class="c-bar-wrap"><div class="c-bar" id="bar-'+i+'" style="width:0%"></div></div>'+
      '<div class="c-val c-bid">'+fmt2(c.bid)+'</div>'+
      '<div class="c-val c-ctr">'+fmt4(c.ctr)+'</div>'+
      '<div class="c-val c-ecpm">'+fmt4(c.ecpm)+'</div>'+
      '<div class="c-status" id="st-'+i+'">—</div>';
    grid.appendChild(row);
  });

  if(animate)await sleep(400);

  // step 1: budget check
  if(animate)setPipeStep(1,'checking','active');
  let budgetPassed=[];
  for(let i=0;i<candidates.length;i++){
    const c=candidates[i];
    if(c.throttled){
      document.getElementById('st-'+i).textContent='THROTTLED';
      document.getElementById('st-'+i).className='c-status throttled';
      document.getElementById('row-'+i).classList.add('eliminated');
      log('  '+c.name+': budget throttled');
    } else {
      budgetPassed.push({...c,idx:i});
    }
    if(animate)await sleep(80);
  }
  if(animate){setPipeStep(1,budgetPassed.length+' pass','active');await sleep(300);}

  // step 2: floor filter
  if(animate)setPipeStep(2,'filtering','active');
  let floorPassed=[];
  for(const c of budgetPassed){
    if(c.bid<floor){
      document.getElementById('st-'+c.idx).textContent='<FLOOR';
      document.getElementById('st-'+c.idx).className='c-status floor';
      document.getElementById('row-'+c.idx).classList.add('eliminated');
      log('  '+c.name+': bid '+c.bid+' < floor '+floor);
    } else {
      floorPassed.push(c);
    }
    if(animate)await sleep(80);
  }
  if(animate){setPipeStep(2,floorPassed.length+' pass','active');await sleep(300);}

  if(floorPassed.length===0){
    resultPanel.className='result-panel';
    resultPanel.innerHTML='<span style="color:var(--red);font-family:\'Share Tech Mono\',monospace;font-size:0.8rem">// NO FILL — all candidates filtered</span>';
    log('  no fill','');
    running=false;return;
  }

  // step 3: ecpm rank
  if(animate)setPipeStep(3,'ranking','active');
  floorPassed.sort((a,b)=>b.ecpm-a.ecpm);
  const maxECPM=floorPassed[0].ecpm;
  for(const c of floorPassed){
    const pct=Math.round((c.ecpm/maxECPM)*100);
    const bar=document.getElementById('bar-'+c.idx);
    bar.style.width=pct+'%';
    document.getElementById('st-'+c.idx).textContent='OK';
    document.getElementById('st-'+c.idx).className='c-status ok';
    if(animate)await sleep(60);
  }
  if(animate){setPipeStep(3,'sorted','active');await sleep(350);}

  // step 4: second price
  if(animate)setPipeStep(4,'selecting','active');
  const winner=floorPassed[0];
  const clearingPrice=floorPassed.length>1
    ? Math.max(floorPassed[1].bid, floor)
    : floor;
  if(animate)await sleep(300);

  // step 5: winner
  setPipeStep(5,winner.name,'active');
  document.getElementById('row-'+winner.idx).classList.add('winner');
  document.getElementById('bar-'+winner.idx).classList.add('winner-bar');
  document.getElementById('st-'+winner.idx).textContent='WINNER';
  document.getElementById('st-'+winner.idx).style.color='var(--gold)';

  log('  winner: '+winner.name+' | eCPM='+fmt4(winner.ecpm)+' | clearing='+fmt2(clearingPrice)+' CPM','win');

  // result panel
  resultPanel.className='result-panel has-result';
  resultPanel.innerHTML=
    '<div class="result-row">'+
    '<div class="result-field"><div class="result-key">winner</div><div class="result-val gold">'+winner.name+'</div></div>'+
    '<div class="result-field"><div class="result-key">eCPM</div><div class="result-val gold">'+fmt4(winner.ecpm)+'</div></div>'+
    '<div class="result-field"><div class="result-key">clearing price</div><div class="result-val">'+fmt2(clearingPrice)+' CPM</div></div>'+
    '<div class="result-field"><div class="result-key">bid</div><div class="result-val">'+fmt2(winner.bid)+'</div></div>'+
    '<div class="result-field"><div class="result-key">predicted CTR</div><div class="result-val">'+fmt4(winner.ctr)+'</div></div>'+
    '<div class="result-field"><div class="result-key">floor</div><div class="result-val">'+fmt2(floor)+' CPM</div></div>'+
    '<div class="result-field"><div class="result-key">candidates in</div><div class="result-val green">'+candidates.length+'</div></div>'+
    '<div class="result-field"><div class="result-key">after filters</div><div class="result-val green">'+floorPassed.length+'</div></div>'+
    '</div>';

  running=false;
}

function startAuction(){
  stopContinuous();
  runAuction(true);
}

function stopAuction(){
  stopContinuous();
  running=false;
}

function runContinuous(){
  if(autoInterval){stopContinuous();return;}
  document.getElementById('btn-cont').textContent='⟳ STOP AUTO';
  document.getElementById('btn-cont').classList.add('running');
  autoInterval=setInterval(()=>{
    if(!running)runAuction(true);
  },2200);
  runAuction(true);
}

function stopContinuous(){
  if(autoInterval){clearInterval(autoInterval);autoInterval=null;}
  document.getElementById('btn-cont').textContent='⟳ AUTO-RUN';
  document.getElementById('btn-cont').classList.remove('running');
}

function copyCode(btn){
  const pre=btn.closest('.code-block');
  const text=pre.innerText.replace('COPY\n','');
  navigator.clipboard.writeText(text).then(()=>{
    btn.textContent='COPIED';setTimeout(()=>btn.textContent='COPY',1500);
  });
}

// run once on load after short delay
setTimeout(()=>runAuction(true),800);
</script>
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
