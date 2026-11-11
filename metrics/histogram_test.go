package metrics

import (
	"strings"
	"testing"
)

func TestHistogramVec_WriteText(t *testing.T) {
	r := NewRegistry()
	h := r.NewHistogramVec("job_seconds", "Job time.", []float64{1, 10}, "repo")
	h.Observe(0.5, "a")
	h.Observe(5, "a")
	h.Observe(50, "a")

	var b strings.Builder
	if err := r.WriteText(&b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"# TYPE job_seconds histogram\n",
		`job_seconds_bucket{repo="a",le="1"} 1` + "\n",
		`job_seconds_bucket{repo="a",le="10"} 2` + "\n",
		`job_seconds_bucket{repo="a",le="+Inf"} 3` + "\n",
		`job_seconds_sum{repo="a"} 55.5` + "\n",
		`job_seconds_count{repo="a"} 3` + "\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if got := h.Count("a"); got != 3 {
		t.Errorf("Count = %d, want 3", got)
	}
}

func TestHistogramVec_NilSafe(t *testing.T) {
	var r *Registry
	h := r.NewHistogramVec("x", "x", DefaultDurationBuckets, "l")
	h.Observe(1, "v")
	if h.Count("v") != 0 {
		t.Error("nil histogram must count nothing")
	}
}
