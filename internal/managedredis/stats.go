package managedredis

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type CommandStat struct {
	Name          string  `json:"name"`
	Calls         int64   `json:"calls"`
	TotalMicros   int64   `json:"totalMicros"`
	MicrosPerCall float64 `json:"microsPerCall"`
	P50Micros     float64 `json:"p50Micros"`
	P95Micros     float64 `json:"p95Micros"`
	P99Micros     float64 `json:"p99Micros"`
}

type KeyspaceStat struct {
	Database   string `json:"database"`
	Keys       int64  `json:"keys"`
	Expires    int64  `json:"expires"`
	AverageTTL int64  `json:"averageTtlMillis"`
}

type SlowlogEntry struct {
	ID              int64  `json:"id"`
	TimestampMillis int64  `json:"timestampMillis"`
	DurationMicros  int64  `json:"durationMicros"`
	Command         string `json:"command"`
	Client          string `json:"client"`
}

type Stats struct {
	Version              string         `json:"version"`
	UptimeSeconds        int64          `json:"uptimeSeconds"`
	ConnectedClients     int64          `json:"connectedClients"`
	BlockedClients       int64          `json:"blockedClients"`
	RejectedConnections  int64          `json:"rejectedConnections"`
	UsedMemoryBytes      int64          `json:"usedMemoryBytes"`
	RSSMemoryBytes       int64          `json:"rssMemoryBytes"`
	PeakMemoryBytes      int64          `json:"peakMemoryBytes"`
	FragmentationRatio   float64        `json:"fragmentationRatio"`
	MaxMemoryBytes       int64          `json:"maxMemoryBytes"`
	EvictionPolicy       string         `json:"evictionPolicy"`
	OperationsPerSecond  int64          `json:"operationsPerSecond"`
	TotalCommands        int64          `json:"totalCommands"`
	TotalConnections     int64          `json:"totalConnections"`
	TotalNetInputBytes   int64          `json:"totalNetInputBytes"`
	TotalNetOutputBytes  int64          `json:"totalNetOutputBytes"`
	KeyspaceHits         int64          `json:"keyspaceHits"`
	KeyspaceMisses       int64          `json:"keyspaceMisses"`
	ExpiredKeys          int64          `json:"expiredKeys"`
	EvictedKeys          int64          `json:"evictedKeys"`
	AOFEnabled           bool           `json:"aofEnabled"`
	LatencyP50Micros     float64        `json:"latencyP50Micros"`
	LatencyP95Micros     float64        `json:"latencyP95Micros"`
	LatencyP99Micros     float64        `json:"latencyP99Micros"`
	Commands             []CommandStat  `json:"commands"`
	Keyspaces            []KeyspaceStat `json:"keyspaces"`
	Slowlog              []SlowlogEntry `json:"slowlog"`
}

func (client *Client) Stats(ctx context.Context) (Stats, error) {
	value, err := client.command(ctx, "INFO", "all")
	if err != nil {
		return Stats{}, err
	}
	if value.kind != responseBulk {
		return Stats{}, errors.New("unexpected Redis INFO response")
	}
	stats, err := parseStats(value.bulk)
	if err != nil {
		return Stats{}, err
	}
	stats.Slowlog = []SlowlogEntry{}
	// SLOWLOG is diagnostic-only; keep INFO-derived stats available when it fails.
	slowlog, err := client.command(ctx, "SLOWLOG", "GET", "32")
	if err != nil {
		return stats, nil
	}
	entries, err := parseSlowlog(slowlog)
	if err != nil {
		return stats, nil
	}
	stats.Slowlog = entries
	return stats, nil
}

func parseStats(payload []byte) (Stats, error) {
	fields := make(map[string]string)
	for _, line := range strings.Split(string(payload), "\r\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if ok && name != "" {
			fields[name] = value
		}
	}
	integer := func(name string) (int64, error) {
		value, ok := fields[name]
		if !ok {
			return 0, fmt.Errorf("Redis INFO lacks %s", name)
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 0 {
			return 0, fmt.Errorf("Redis INFO %s is invalid", name)
		}
		return parsed, nil
	}
	result := Stats{Version: fields["redis_version"], EvictionPolicy: fields["maxmemory_policy"]}
	if result.Version == "" || result.EvictionPolicy == "" {
		return Stats{}, errors.New("Redis INFO lacks server or memory fields")
	}
	var err error
	values := []*int64{
		&result.UptimeSeconds, &result.ConnectedClients, &result.BlockedClients,
		&result.RejectedConnections, &result.UsedMemoryBytes, &result.RSSMemoryBytes,
		&result.PeakMemoryBytes, &result.MaxMemoryBytes, &result.OperationsPerSecond,
		&result.TotalCommands, &result.TotalConnections, &result.TotalNetInputBytes,
		&result.TotalNetOutputBytes, &result.KeyspaceHits, &result.KeyspaceMisses,
		&result.ExpiredKeys, &result.EvictedKeys,
	}
	names := []string{
		"uptime_in_seconds", "connected_clients", "blocked_clients", "rejected_connections",
		"used_memory", "used_memory_rss", "used_memory_peak", "maxmemory",
		"instantaneous_ops_per_sec", "total_commands_processed", "total_connections_received",
		"total_net_input_bytes", "total_net_output_bytes",
		"keyspace_hits", "keyspace_misses", "expired_keys", "evicted_keys",
	}
	for index, name := range names {
		*values[index], err = integer(name)
		if err != nil {
			return Stats{}, err
		}
	}
	result.FragmentationRatio, err = strconv.ParseFloat(fields["mem_fragmentation_ratio"], 64)
	if err != nil || result.FragmentationRatio < 0 {
		return Stats{}, errors.New("Redis INFO memory fragmentation ratio is invalid")
	}
	aof, err := integer("aof_enabled")
	if err != nil || (aof != 0 && aof != 1) {
		return Stats{}, errors.New("Redis INFO AOF state is invalid")
	}
	result.AOFEnabled = aof == 1
	latencies := make(map[string]latencyPercentiles)
	for name, value := range fields {
		if strings.HasPrefix(name, "cmdstat_") {
			stat, parseErr := parseCommandStat(strings.TrimPrefix(name, "cmdstat_"), value)
			if parseErr != nil {
				return Stats{}, parseErr
			}
			result.Commands = append(result.Commands, stat)
		}
		if strings.HasPrefix(name, "latency_percentiles_usec_") {
			command := strings.TrimPrefix(name, "latency_percentiles_usec_")
			percentiles, parseErr := parseLatencyPercentiles(value)
			if parseErr != nil {
				return Stats{}, fmt.Errorf("Redis latency percentiles for %s are invalid", command)
			}
			latencies[command] = percentiles
		}
		if isRedisDatabaseName(name) {
			stat, parseErr := parseKeyspaceStat(name, value)
			if parseErr != nil {
				return Stats{}, parseErr
			}
			result.Keyspaces = append(result.Keyspaces, stat)
		}
	}
	var weightedCalls int64
	for index := range result.Commands {
		percentiles, ok := latencies[result.Commands[index].Name]
		if !ok {
			continue
		}
		result.Commands[index].P50Micros = percentiles.p50
		result.Commands[index].P95Micros = percentiles.p95
		result.Commands[index].P99Micros = percentiles.p99
		calls := result.Commands[index].Calls
		weightedCalls += calls
		result.LatencyP50Micros += percentiles.p50 * float64(calls)
		result.LatencyP95Micros += percentiles.p95 * float64(calls)
		result.LatencyP99Micros += percentiles.p99 * float64(calls)
	}
	if weightedCalls > 0 {
		weight := float64(weightedCalls)
		result.LatencyP50Micros /= weight
		result.LatencyP95Micros /= weight
		result.LatencyP99Micros /= weight
	}
	sort.Slice(result.Commands, func(left, right int) bool {
		return result.Commands[left].TotalMicros > result.Commands[right].TotalMicros
	})
	sort.Slice(result.Keyspaces, func(left, right int) bool { return result.Keyspaces[left].Database < result.Keyspaces[right].Database })
	return result, nil
}

type latencyPercentiles struct {
	p50 float64
	p95 float64
	p99 float64
}

func parseLatencyPercentiles(value string) (latencyPercentiles, error) {
	fields := commaFields(value)
	parse := func(keys ...string) (float64, bool, error) {
		for _, key := range keys {
			raw, ok := fields[key]
			if !ok {
				continue
			}
			parsed, err := strconv.ParseFloat(raw, 64)
			if err != nil || parsed < 0 {
				return 0, false, errors.New("invalid percentile")
			}
			return parsed, true, nil
		}
		return 0, false, nil
	}
	p50, _, err := parse("p50", "p50.0")
	if err != nil {
		return latencyPercentiles{}, err
	}
	p95, _, err := parse("p95", "p95.0")
	if err != nil {
		return latencyPercentiles{}, err
	}
	p99, _, err := parse("p99", "p99.0")
	if err != nil {
		return latencyPercentiles{}, err
	}
	return latencyPercentiles{p50: p50, p95: p95, p99: p99}, nil
}

func parseSlowlog(value response) ([]SlowlogEntry, error) {
	if value.kind != responseArray {
		return nil, errors.New("unexpected Redis SLOWLOG response")
	}
	entries := make([]SlowlogEntry, 0, len(value.array))
	for _, item := range value.array {
		entry, err := parseSlowlogEntry(item)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func parseSlowlogEntry(value response) (SlowlogEntry, error) {
	if value.kind != responseArray || len(value.array) < 4 {
		return SlowlogEntry{}, errors.New("Redis SLOWLOG entry has an unexpected shape")
	}
	id, err := responseIntegerValue(value.array[0])
	if err != nil || id < 0 {
		return SlowlogEntry{}, errors.New("Redis SLOWLOG entry id is invalid")
	}
	timestampSeconds, err := responseIntegerValue(value.array[1])
	if err != nil || timestampSeconds < 0 {
		return SlowlogEntry{}, errors.New("Redis SLOWLOG entry timestamp is invalid")
	}
	duration, err := responseIntegerValue(value.array[2])
	if err != nil || duration < 0 {
		return SlowlogEntry{}, errors.New("Redis SLOWLOG entry duration is invalid")
	}
	command, err := formatSlowlogCommand(value.array[3])
	if err != nil {
		return SlowlogEntry{}, err
	}
	client := ""
	if len(value.array) >= 5 {
		client, err = responseBulkString(value.array[4])
		if err != nil {
			return SlowlogEntry{}, errors.New("Redis SLOWLOG entry client is invalid")
		}
	}
	return SlowlogEntry{
		ID:              id,
		TimestampMillis: timestampSeconds * 1000,
		DurationMicros:  duration,
		Command:         command,
		Client:          client,
	}, nil
}

func formatSlowlogCommand(value response) (string, error) {
	if value.kind != responseArray {
		return "", errors.New("Redis SLOWLOG command is invalid")
	}
	parts := make([]string, 0, len(value.array))
	for _, argument := range value.array {
		text, err := responseBulkString(argument)
		if err != nil {
			return "", errors.New("Redis SLOWLOG command argument is invalid")
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, " "), nil
}

func responseIntegerValue(value response) (int64, error) {
	if value.kind != responseInteger {
		return 0, errors.New("expected Redis integer")
	}
	return value.integer, nil
}

func responseBulkString(value response) (string, error) {
	if value.kind != responseBulk {
		return "", errors.New("expected Redis bulk string")
	}
	return string(value.bulk), nil
}

func isRedisDatabaseName(name string) bool {
	if len(name) <= 2 || name[0] != 'd' || name[1] != 'b' {
		return false
	}
	for index := 2; index < len(name); index++ {
		if name[index] < '0' || name[index] > '9' {
			return false
		}
	}
	return true
}

func parseCommandStat(name, value string) (CommandStat, error) {
	fields := commaFields(value)
	calls, callsErr := strconv.ParseInt(fields["calls"], 10, 64)
	total, totalErr := strconv.ParseInt(fields["usec"], 10, 64)
	average, averageErr := strconv.ParseFloat(fields["usec_per_call"], 64)
	if callsErr != nil || totalErr != nil || averageErr != nil || calls < 0 || total < 0 || average < 0 {
		return CommandStat{}, fmt.Errorf("Redis command stats for %s are invalid", name)
	}
	return CommandStat{Name: name, Calls: calls, TotalMicros: total, MicrosPerCall: average}, nil
}

func parseKeyspaceStat(name, value string) (KeyspaceStat, error) {
	fields := commaFields(value)
	keys, keysErr := strconv.ParseInt(fields["keys"], 10, 64)
	expires, expiresErr := strconv.ParseInt(fields["expires"], 10, 64)
	average, averageErr := strconv.ParseInt(fields["avg_ttl"], 10, 64)
	if keysErr != nil || expiresErr != nil || averageErr != nil || keys < 0 || expires < 0 || average < 0 {
		return KeyspaceStat{}, fmt.Errorf("Redis keyspace stats for %s are invalid", name)
	}
	return KeyspaceStat{Database: name, Keys: keys, Expires: expires, AverageTTL: average}, nil
}

func commaFields(value string) map[string]string {
	result := make(map[string]string)
	for _, field := range strings.Split(value, ",") {
		name, item, ok := strings.Cut(field, "=")
		if ok {
			result[name] = item
		}
	}
	return result
}
