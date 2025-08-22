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

