package traffic

import "testing"

func statByName(stats []EgressStat, name string) (EgressStat, bool) {
	for _, s := range stats {
		if s.ContainerName == name {
			return s, true
		}
	}
	return EgressStat{}, false
}

func TestAggregateEgress_DistinctDestinationsAndCount(t *testing.T) {
	ids := map[string]string{"crawler": "uuid-c", "normal": "uuid-n"}
	idForName := func(n string) string { return ids[n] }

	conns := []egressConn{
		// "crawler": 5 connections to 4 distinct destinations (one repeat).
		{"crawler", "1.1.1.1"},
		{"crawler", "2.2.2.2"},
		{"crawler", "3.3.3.3"},
		{"crawler", "4.4.4.4"},
		{"crawler", "1.1.1.1"}, // repeat dst — still one distinct
		// "normal": 3 connections to 1 distinct destination (an upstream API).
		{"normal", "10.0.0.9"},
		{"normal", "10.0.0.9"},
		{"normal", "10.0.0.9"},
	}

	stats := aggregateEgress(conns, idForName)
	if len(stats) != 2 {
		t.Fatalf("len(stats) = %d, want 2 (%+v)", len(stats), stats)
	}
