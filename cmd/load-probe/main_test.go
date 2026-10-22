package main

import (
	"net/netip"
	"testing"
)

func TestSyntheticIPsAreDistinctBenchmarkAddresses(t *testing.T) {
	benchmark := netip.MustParsePrefix("198.18.0.0/15")
	seen := map[string]bool{}
	for i := 0; i < 70000; i++ {
		ip := syntheticIP(i)
		addr, err := netip.ParseAddr(ip)
		if err != nil || !benchmark.Contains(addr) || seen[ip] {
			t.Fatalf("index %d gave %q (err %v, duplicate %v)", i, ip, err, seen[ip])
		}
		seen[ip] = true
	}
}

func TestPercentileRoundsToNearestPosition(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if got := percentile(values, .5); got != 6 {
		t.Fatalf("p50 = %v", got)
	}
	if got := percentile(values, .99); got != 10 {
		t.Fatalf("p99 = %v", got)
	}
	if got := percentile(nil, .5); got != 0 {
		t.Fatalf("empty = %v", got)
	}
}
