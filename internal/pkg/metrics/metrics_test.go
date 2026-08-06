package metrics

import (
	"strings"
	"testing"
)

func TestObserve_CountsAndSum(t *testing.T) {
	r := NewRegistry()
	r.Observe("GET", "/api/v1/invoices", 200, 0.010)
	r.Observe("GET", "/api/v1/invoices", 200, 0.020)
	r.Observe("GET", "/api/v1/invoices", 200, 0.030)

	snap := r.Snapshot()
	if snap.Total != 3 {
		t.Errorf("total = %d, want 3", snap.Total)
	}
	if got, want := snap.SumSeconds, 0.060; got < want-1e-9 || got > want+1e-9 {
		t.Errorf("sum = %v, want %v", got, want)
	}
}

// The §5.1 target is the whole reason this package exists, so the boundary itself must
// be classified correctly.
func TestSnapshot_SLOBoundary(t *testing.T) {
	r := NewRegistry()

	// Well inside the target.
	r.Observe("GET", "/x", 200, 0.050)
	// Exactly at the 300 ms bucket bound — counts as within.
	r.Observe("GET", "/x", 200, 0.300)
	// Just past it.
	r.Observe("GET", "/x", 200, 0.301)
	// Far past it.
	r.Observe("GET", "/x", 200, 2.000)

	snap := r.Snapshot()
	if snap.Total != 4 {
		t.Fatalf("total = %d, want 4", snap.Total)
	}
	if snap.WithinSLO != 2 {
		t.Errorf("within SLO = %d, want 2 (50ms and 300ms)", snap.WithinSLO)
	}
}

func TestSnapshot_EmptyRegistry(t *testing.T) {
	snap := NewRegistry().Snapshot()
	if snap.Total != 0 || snap.WithinSLO != 0 || snap.SumSeconds != 0 {
		t.Errorf("empty registry should be all zero, got %+v", snap)
	}
}

// Prometheus histogram buckets are cumulative: each le=X must include everything below.
func TestRender_BucketsAreCumulative(t *testing.T) {
	r := NewRegistry()
	r.Observe("GET", "/x", 200, 0.004) // first bucket (le=0.005)
	r.Observe("GET", "/x", 200, 0.007) // le=0.010
	r.Observe("GET", "/x", 200, 9.000) // beyond the last bound -> +Inf only

	out := r.Render()

	for _, want := range []string{
		`http_request_duration_seconds_bucket{method="GET",route="/x",status="200",le="0.005"} 1`,
		`http_request_duration_seconds_bucket{method="GET",route="/x",status="200",le="0.01"} 2`,
		`http_request_duration_seconds_bucket{method="GET",route="/x",status="200",le="5"} 2`,
		`http_request_duration_seconds_bucket{method="GET",route="/x",status="200",le="+Inf"} 3`,
		`http_request_duration_seconds_count{method="GET",route="/x",status="200"} 3`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing line:\n  %s\ngot:\n%s", want, out)
		}
	}
}

func TestRender_ExpositionFormatHeaders(t *testing.T) {
	r := NewRegistry()
	r.Observe("GET", "/x", 200, 0.1)

	out := r.Render()
	for _, want := range []string{
		"# HELP http_request_duration_seconds",
		"# TYPE http_request_duration_seconds histogram",
		"# TYPE http_requests_within_slo_ratio gauge",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestRender_SLORatio(t *testing.T) {
	r := NewRegistry()
	// 3 of 4 inside the target.
	r.Observe("GET", "/x", 200, 0.010)
	r.Observe("GET", "/x", 200, 0.020)
	r.Observe("GET", "/x", 200, 0.030)
	r.Observe("GET", "/x", 200, 1.500)

	if out := r.Render(); !strings.Contains(out, "http_requests_within_slo_ratio 0.75") {
		t.Errorf("expected a 0.75 ratio in:\n%s", out)
	}
}

// An empty registry reports a ratio of 1 rather than NaN — a division by zero here
// would break the scrape.
func TestRender_EmptyRegistryRatioIsOne(t *testing.T) {
	if out := NewRegistry().Render(); !strings.Contains(out, "http_requests_within_slo_ratio 1") {
		t.Errorf("empty registry should report ratio 1, got:\n%s", out)
	}
}

// Distinct label sets are distinct series.
func TestObserve_SeriesAreSeparatedByLabels(t *testing.T) {
	r := NewRegistry()
	r.Observe("GET", "/a", 200, 0.01)
	r.Observe("GET", "/a", 500, 0.01) // different status
	r.Observe("POST", "/a", 200, 0.01)
	r.Observe("GET", "/b", 200, 0.01) // different route

	out := r.Render()
	for _, want := range []string{
		`route="/a",status="200"`,
		`route="/a",status="500"`,
		`method="POST",route="/a"`,
		`route="/b"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing series %q", want)
		}
	}
	if snap := r.Snapshot(); snap.Total != 4 {
		t.Errorf("total = %d, want 4 across all series", snap.Total)
	}
}

// An unmatched route arrives as "" from gin. Labelling it as-is would emit an empty
// label; folding it into one bucket also keeps 404 scans from inflating cardinality.
func TestObserve_EmptyRouteFoldsIntoUnmatched(t *testing.T) {
	r := NewRegistry()
	r.Observe("GET", "", 404, 0.001)
	r.Observe("GET", "", 404, 0.002)

	out := r.Render()
	if !strings.Contains(out, `route="unmatched"`) {
		t.Errorf("expected an \"unmatched\" route label in:\n%s", out)
	}
	if strings.Contains(out, `route=""`) {
		t.Error("empty route label must not be emitted")
	}
	if !strings.Contains(out, `http_request_duration_seconds_count{method="GET",route="unmatched",status="404"} 2`) {
		t.Error("both unmatched requests should share one series")
	}
}

// Output ordering must be stable so scrapes and test assertions are reproducible.
func TestRender_IsDeterministic(t *testing.T) {
	build := func() string {
		r := NewRegistry()
		r.Observe("POST", "/z", 201, 0.01)
		r.Observe("GET", "/a", 200, 0.02)
		r.Observe("GET", "/m", 500, 0.03)
		return r.Render()
	}
	if build() != build() {
		t.Error("Render output is not deterministic")
	}
}

func TestSLOSecondsMatchesABucketBound(t *testing.T) {
	// If the SLO were not itself a bucket bound, WithinSLO would have to interpolate
	// and the number would be an estimate rather than exact.
	for _, b := range latencyBuckets {
		if b == SLOSeconds {
			return
		}
	}
	t.Errorf("SLOSeconds (%v) must be one of the bucket bounds %v", SLOSeconds, latencyBuckets)
}
