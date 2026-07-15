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
		"total_connections_received:80", "rejected_connections:2", "keyspace_hits:900",
		"keyspace_misses:100", "expired_keys:20", "evicted_keys:0",
		"cmdstat_get:calls=3000,usec=9000,usec_per_call=3.00",
		"# Keyspace", "db0:keys=40,expires=5,avg_ttl=60000", "",
	}
	stats, err := parseStats([]byte(strings.Join(lines, "\r\n")))
	if err != nil {
		t.Fatal(err)
	}
	if stats.Version != "8.2.1" || !stats.AOFEnabled || stats.ConnectedClients != 12 {
		t.Fatalf("summary = %+v", stats)
	}
	if len(stats.Commands) != 1 || stats.Commands[0].Name != "get" || stats.Commands[0].Calls != 3000 {
		t.Fatalf("commands = %+v", stats.Commands)
	}
	if len(stats.Keyspaces) != 1 || stats.Keyspaces[0].Database != "db0" || stats.Keyspaces[0].AverageTTL != 60_000 {
		t.Fatalf("keyspaces = %+v", stats.Keyspaces)
	}
}
