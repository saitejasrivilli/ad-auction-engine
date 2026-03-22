// Package metrics registers and exposes Prometheus metrics for the auction service.
//
// Metrics exposed:
//   auction_requests_total          counter   — total auctions attempted
//   auction_fills_total             counter   — auctions with a winner
//   auction_no_fill_total           counter   — auctions with no valid candidate
//   auction_latency_us              histogram — per-auction latency in microseconds
//   auction_clearing_price_cpm      histogram — winning clearing price distribution
//   auction_ecpm                    histogram — winner eCPM distribution
//   auction_budget_throttle_total   counter   — candidates dropped by budget pacer
//   auction_floor_filter_total      counter   — candidates dropped by floor price
//   auction_revenue_per_mille       gauge     — rolling RPM estimate
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	AuctionRequestsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "auction_requests_total",
		Help: "Total number of auction requests.",
	})

	AuctionFillsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "auction_fills_total",
		Help: "Total auctions with at least one valid winner.",
	})

	AuctionNoFillTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "auction_no_fill_total",
		Help: "Total auctions with no eligible candidate.",
	})

	AuctionLatencyUS = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "auction_latency_us",
		Help:    "Per-auction latency in microseconds.",
		Buckets: prometheus.ExponentialBuckets(100, 2, 14), // 100µs → ~800ms
	})

	AuctionClearingPriceCPM = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "auction_clearing_price_cpm",
		Help:    "Distribution of clearing prices (CPM).",
		Buckets: prometheus.LinearBuckets(0, 0.5, 40), // $0 – $20 CPM
	})

	AuctionECPM = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "auction_ecpm",
		Help:    "Distribution of winner eCPM values.",
		Buckets: prometheus.LinearBuckets(0, 0.25, 40),
	})

	AuctionBudgetThrottleTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "auction_budget_throttle_total",
		Help: "Candidates dropped by budget pacer.",
	})

	AuctionFloorFilterTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "auction_floor_filter_total",
		Help: "Candidates dropped by floor price.",
	})

	AuctionRevenuePerMille = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "auction_revenue_per_mille",
		Help: "Rolling revenue per mille impressions (RPM).",
	})
)
