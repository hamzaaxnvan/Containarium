package safecast

import (
	"math"
	"testing"
)

func TestI32(t *testing.T) {
	cases := []struct {
		in   int64
		want int32
	}{
		{0, 0},
		{42, 42},
		{-42, -42},
		{math.MaxInt32, math.MaxInt32},
		{math.MinInt32, math.MinInt32},
		{math.MaxInt32 + 1, math.MaxInt32}, // saturate high
		{math.MinInt32 - 1, math.MinInt32}, // saturate low
		{math.MaxInt64, math.MaxInt32},
		{math.MinInt64, math.MinInt32},
	}
	for _, c := range cases {
		if got := I32(c.in); got != c.want {
			t.Errorf("I32(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestI64FromU64(t *testing.T) {
