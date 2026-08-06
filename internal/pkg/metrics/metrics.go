// Package metrics records request latency and exposes it in Prometheus text format.
//
// It exists to make SRS §5.1 ("response API normal ≤ 300 ms") measurable rather than
// aspirational: without a latency histogram there is no way to tell whether the target
// is met. Implemented in-process with no external dependency — the exposition format is
// standard, so a real Prometheus can scrape it unchanged.
package metrics

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// latencyBuckets are cumulative upper bounds in seconds, deliberately dense around the
// 300 ms SLO from §5.1 so the boundary itself is observable rather than interpolated.
// Declared as an array, not a slice, so len() is a compile-time constant and the
// per-series bucket counters can be a fixed-size array too.
var latencyBuckets = [...]float64{
	0.005, 0.010, 0.025, 0.050, 0.100, 0.200,
	0.300, // <-- the SRS §5.1 target
	0.500, 1.000, 2.500, 5.000,
}

// SLOSeconds is the §5.1 latency target, exposed so the ratio of requests within it can
// be reported directly.
const SLOSeconds = 0.300

type labels struct {
	method string
	route  string
	status int
}

type histogram struct {
	counts [len(latencyBuckets)]uint64 // per-bucket (non-cumulative; summed at render)
	inf    uint64
	sum    float64
	total  uint64
}

// Registry collects latency observations keyed by method/route/status.
type Registry struct {
	mu    sync.Mutex
	hists map[labels]*histogram
}

func NewRegistry() *Registry {
	return &Registry{hists: make(map[labels]*histogram)}
}

// Observe records one request. `route` must be the matched route *pattern*
// (e.g. "/api/v1/invoices/:id"), never the concrete path — using the concrete path
// would create one time series per invoice id and blow up cardinality.
func (r *Registry) Observe(method, route string, status int, seconds float64) {
	if route == "" {
		route = "unmatched"
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	key := labels{method: method, route: route, status: status}
	h, ok := r.hists[key]
	if !ok {
		h = &histogram{}
		r.hists[key] = h
	}

	h.total++
	h.sum += seconds

	idx := sort.SearchFloat64s(latencyBuckets[:], seconds)
	// SearchFloat64s returns the first index where buckets[i] >= seconds. Prometheus
	// buckets are "less than or equal", which is what we want, so that index is the
	// bucket this observation falls into.
	if idx < len(latencyBuckets) {
		h.counts[idx]++
	} else {
		h.inf++
	}
}

// Snapshot is an aggregate view across every series, used by tests and by the
// human-readable SLO summary.
type Snapshot struct {
	Total      uint64
	WithinSLO  uint64
	SumSeconds float64
}

// Snapshot aggregates all series. WithinSLO counts observations that landed in a bucket
// whose upper bound is <= the §5.1 target.
func (r *Registry) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	var out Snapshot
	for _, h := range r.hists {
		out.Total += h.total
		out.SumSeconds += h.sum
		for i, ub := range latencyBuckets {
			if ub <= SLOSeconds {
				out.WithinSLO += h.counts[i]
			}
		}
	}
	return out
}

// Render writes the Prometheus text exposition format.
func (r *Registry) Render() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	var b strings.Builder
	b.WriteString("# HELP http_request_duration_seconds Request latency in seconds.\n")
	b.WriteString("# TYPE http_request_duration_seconds histogram\n")

	// Deterministic ordering keeps the output diffable and the tests stable.
	keys := make([]labels, 0, len(r.hists))
	for k := range r.hists {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].route != keys[j].route {
			return keys[i].route < keys[j].route
		}
		if keys[i].method != keys[j].method {
			return keys[i].method < keys[j].method
		}
		return keys[i].status < keys[j].status
	})

	for _, k := range keys {
		h := r.hists[k]
		lbl := fmt.Sprintf(`method=%q,route=%q,status="%d"`, k.method, k.route, k.status)

		// Histogram buckets are cumulative in the exposition format.
		var cumulative uint64
		for i, ub := range latencyBuckets {
			cumulative += h.counts[i]
			fmt.Fprintf(&b, "http_request_duration_seconds_bucket{%s,le=\"%s\"} %d\n",
				lbl, strconv.FormatFloat(ub, 'g', -1, 64), cumulative)
		}
		cumulative += h.inf
		fmt.Fprintf(&b, "http_request_duration_seconds_bucket{%s,le=\"+Inf\"} %d\n", lbl, cumulative)
		fmt.Fprintf(&b, "http_request_duration_seconds_sum{%s} %s\n",
			lbl, strconv.FormatFloat(h.sum, 'g', -1, 64))
		fmt.Fprintf(&b, "http_request_duration_seconds_count{%s} %d\n", lbl, h.total)
	}

	// A derived gauge so the §5.1 target can be read without a PromQL query.
	snap := Snapshot{}
	for _, h := range r.hists {
		snap.Total += h.total
		for i, ub := range latencyBuckets {
			if ub <= SLOSeconds {
				snap.WithinSLO += h.counts[i]
			}
		}
	}
	ratio := 1.0
	if snap.Total > 0 {
		ratio = float64(snap.WithinSLO) / float64(snap.Total)
	}
	b.WriteString("# HELP http_requests_within_slo_ratio Fraction of requests served within the 300ms SRS §5.1 target.\n")
	b.WriteString("# TYPE http_requests_within_slo_ratio gauge\n")
	fmt.Fprintf(&b, "http_requests_within_slo_ratio %s\n", strconv.FormatFloat(ratio, 'g', -1, 64))

	return b.String()
}
