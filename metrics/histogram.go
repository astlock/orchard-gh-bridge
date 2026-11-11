package metrics

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// DefaultDurationBuckets are the bucket bounds, in seconds, that GitHub's
// Actions Runner Controller uses for its job duration histograms, so the same
// dashboard queries work against both.
var DefaultDurationBuckets = []float64{
	0.01, 0.05, 0.1, 0.5, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 12, 15, 18, 20, 25, 30, 40, 50, 60,
	70, 80, 90, 100, 110, 120, 150, 180, 210, 240, 300, 360, 420, 480, 540, 600, 900, 1200,
	1800, 2400, 3000, 3600,
}

// HistogramVec is a cumulative histogram partitioned by a fixed set of label
// names.
type HistogramVec struct {
	v       *vec
	buckets []float64
}

type histogram struct {
	counts []uint64
	sum    float64
	count  uint64
}

// NewHistogramVec registers a histogram with the given upper bucket bounds.
// A nil registry returns a nil (no-op) histogram.
func (r *Registry) NewHistogramVec(name, help string, buckets []float64, labelNames ...string) *HistogramVec {
	if r == nil {
		return nil
	}
	b := append([]float64(nil), buckets...)
	sort.Float64s(b)
	h := &HistogramVec{v: r.register(name, help, "histogram", labelNames), buckets: b}
	h.v.buckets = b
	return h
}

// Observe records one value for the given label values, in labelNames order.
func (h *HistogramVec) Observe(value float64, labelValues ...string) {
	if h == nil || math.IsNaN(value) {
		return
	}
	h.v.update(labelValues, func(s *series) {
		if s.hist == nil {
			s.hist = &histogram{counts: make([]uint64, len(h.buckets))}
		}
		for i, upper := range h.buckets {
			if value <= upper {
				s.hist.counts[i]++
			}
		}
		s.hist.sum += value
		s.hist.count++
	})
}

// Count returns how many values were observed for the given label values.
// Intended for tests.
func (h *HistogramVec) Count(labelValues ...string) uint64 {
	if h == nil {
		return 0
	}
	h.v.mu.Lock()
	defer h.v.mu.Unlock()
	s, ok := h.v.series[seriesKey(labelValues)]
	if !ok || s.hist == nil {
		return 0
	}
	return s.hist.count
}

func writeHistogram(b *strings.Builder, v *vec, s *series) {
	if s.hist == nil {
		return
	}
	labels := func(extra string) string {
		parts := make([]string, 0, len(v.labelNames)+1)
		for i, ln := range v.labelNames {
			parts = append(parts, fmt.Sprintf("%s=\"%s\"", ln, escapeLabelValue(s.labelValues[i])))
		}
		if extra != "" {
			parts = append(parts, extra)
		}
		if len(parts) == 0 {
			return ""
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	for i, upper := range v.buckets {
		le := fmt.Sprintf("le=\"%s\"", strconv.FormatFloat(upper, 'g', -1, 64))
		fmt.Fprintf(b, "%s_bucket%s %d\n", v.name, labels(le), s.hist.counts[i])
	}
	fmt.Fprintf(b, "%s_bucket%s %d\n", v.name, labels(`le="+Inf"`), s.hist.count)
	fmt.Fprintf(b, "%s_sum%s %s\n", v.name, labels(""), strconv.FormatFloat(s.hist.sum, 'g', -1, 64))
	fmt.Fprintf(b, "%s_count%s %d\n", v.name, labels(""), s.hist.count)
}
