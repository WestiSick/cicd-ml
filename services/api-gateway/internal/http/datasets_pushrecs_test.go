package http

import (
	"math"
	"testing"
)

// pctDelta is the bedrock of the "when to push" colour scale —
// off-by-one in sign or NaN propagation would silently miscolour
// every cell. Cheap to lock down with a table test.
func TestPctDelta(t *testing.T) {
	tests := []struct {
		name        string
		value, base float64
		want        float64
	}{
		{"faster than baseline → negative", 80, 100, -20},
		{"slower than baseline → positive", 150, 100, 50},
		{"equal → zero", 100, 100, 0},
		{"zero baseline guards against div-by-zero", 50, 0, 0},
		{"negative baseline guards", 50, -10, 0},
		{"NaN baseline guards", 50, math.NaN(), 0},
		{"Inf baseline guards", 50, math.Inf(1), 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pctDelta(tc.value, tc.base)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("pctDelta(%v, %v) = %v; want %v", tc.value, tc.base, got, tc.want)
			}
		})
	}
}

// validTZ guards user input that flows into Postgres `AT TIME ZONE` —
// any character class that lets through whitespace or quoting would
// be a 500 (Postgres errors on bad zones) rather than a clean 400.
func TestValidTZ(t *testing.T) {
	good := []string{
		"UTC", "GMT", "Europe/Moscow", "America/Argentina/Buenos_Aires",
		"Etc/GMT+3", "Etc/GMT-5",
	}
	for _, s := range good {
		if !validTZ.MatchString(s) {
			t.Errorf("expected %q to be accepted", s)
		}
	}
	bad := []string{
		"", " UTC", "UTC ", "Europe/Moscow; DROP TABLE jobs",
		"'UTC'", "Europe/Москва", "UTC\x00",
	}
	for _, s := range bad {
		if validTZ.MatchString(s) {
			t.Errorf("expected %q to be rejected", s)
		}
	}
}

// MeanTotal on both the overall and per-cell structs is the metric
// the heatmap delta is calculated against — make sure it's just the
// sum and not, say, an erroneous average.
func TestMeanTotalIsSum(t *testing.T) {
	o := pushRecsOverall{MeanWait: 12.5, MeanDuration: 240.0}
	if got := o.MeanTotal(); got != 252.5 {
		t.Fatalf("overall MeanTotal = %v; want 252.5", got)
	}
	c := pushRecsCell{MeanWait: 7.0, MeanDuration: 33.0}
	if got := c.MeanTotal(); got != 40.0 {
		t.Fatalf("cell MeanTotal = %v; want 40.0", got)
	}
}
