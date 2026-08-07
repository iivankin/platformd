package managedredis

import (
	"strings"
	"testing"
)

func TestParseStatsIncludesCommandsAndKeyspaces(t *testing.T) {
	lines := []string{
		"# Server", "redis_version:8.2.1", "uptime_in_seconds:3600",
		"# Clients", "connected_clients:12", "blocked_clients:1",
		"# Memory", "used_memory:100", "used_memory_rss:120", "used_memory_peak:140",
		"maxmemory:0", "maxmemory_policy:noeviction", "mem_fragmentation_ratio:1.2",
		"# Persistence", "aof_enabled:1",
		"# Stats", "instantaneous_ops_per_sec:50", "total_commands_processed:5000",
		"total_connections_received:80", "rejected_connections:2",
		"total_net_input_bytes:1000", "total_net_output_bytes:2000",
		"keyspace_hits:900", "keyspace_misses:100", "expired_keys:20", "evicted_keys:0",
		"cmdstat_get:calls=3000,usec=9000,usec_per_call=3.00",
		"cmdstat_set:calls=1000,usec=20000,usec_per_call=20.00",
		"# Latencystats",
		"latency_percentiles_usec_get:p50=1.000,p95=2.000,p99=3.000",
		"latency_percentiles_usec_set:p50=4.000,p95=5.000,p99=6.000",
		"# Keyspace", "db0:keys=40,expires=5,avg_ttl=60000",
		"db0_distrib_lists_items:1=1,2=3", "db0_distrib_sets_items:1=2", "",
	}
	stats, err := parseStats([]byte(strings.Join(lines, "\r\n")))
	if err != nil {
		t.Fatal(err)
	}
	if stats.Version != "8.2.1" || !stats.AOFEnabled || stats.ConnectedClients != 12 {
		t.Fatalf("summary = %+v", stats)
	}
	if stats.TotalNetInputBytes != 1000 || stats.TotalNetOutputBytes != 2000 {
		t.Fatalf("net bytes = %+v", stats)
	}
	if len(stats.Commands) != 2 || stats.Commands[0].Name != "set" || stats.Commands[0].TotalMicros != 20_000 {
		t.Fatalf("commands sorted by totalMicros = %+v", stats.Commands)
	}
	if stats.Commands[1].Name != "get" || stats.Commands[1].P50Micros != 1 || stats.Commands[1].P99Micros != 3 {
		t.Fatalf("get latency = %+v", stats.Commands[1])
	}
	if stats.Commands[0].P50Micros != 4 || stats.Commands[0].P95Micros != 5 {
		t.Fatalf("set latency = %+v", stats.Commands[0])
	}
	// Call-weighted: (1*3000 + 4*1000) / 4000 = 1.75, etc.
	if stats.LatencyP50Micros != 1.75 || stats.LatencyP95Micros != 2.75 || stats.LatencyP99Micros != 3.75 {
		t.Fatalf("overall latency = p50=%v p95=%v p99=%v", stats.LatencyP50Micros, stats.LatencyP95Micros, stats.LatencyP99Micros)
	}
	if len(stats.Keyspaces) != 1 || stats.Keyspaces[0].Database != "db0" || stats.Keyspaces[0].AverageTTL != 60_000 {
		t.Fatalf("keyspaces = %+v", stats.Keyspaces)
	}
}

func TestParseSlowlog(t *testing.T) {
	entries, err := parseSlowlog(response{
		kind: responseArray,
		array: []response{{
			kind: responseArray,
			array: []response{
				{kind: responseInteger, integer: 14},
				{kind: responseInteger, integer: 1_309_448_221},
				{kind: responseInteger, integer: 15_000},
				{
					kind: responseArray,
					array: []response{
						{kind: responseBulk, bulk: []byte("EVAL")},
						{kind: responseBulk, bulk: []byte("return 1")},
						{kind: responseBulk, bulk: []byte("0")},
					},
				},
				{kind: responseBulk, bulk: []byte("127.0.0.1:32321")},
				{kind: responseBulk, bulk: []byte("")},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %+v", entries)
	}
	entry := entries[0]
	if entry.ID != 14 || entry.TimestampMillis != 1_309_448_221_000 || entry.DurationMicros != 15_000 {
		t.Fatalf("entry = %+v", entry)
	}
	if entry.Command != "EVAL return 1 0" || entry.Client != "127.0.0.1:32321" {
		t.Fatalf("entry command/client = %+v", entry)
	}
}
