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
}

type KeyspaceStat struct {
	Database   string `json:"database"`
	Keys       int64  `json:"keys"`
	Expires    int64  `json:"expires"`
	AverageTTL int64  `json:"averageTtlMillis"`
}

type Stats struct {
	Version             string         `json:"version"`
	UptimeSeconds       int64          `json:"uptimeSeconds"`
	ConnectedClients    int64          `json:"connectedClients"`
	BlockedClients      int64          `json:"blockedClients"`
	RejectedConnections int64          `json:"rejectedConnections"`
	UsedMemoryBytes     int64          `json:"usedMemoryBytes"`
	RSSMemoryBytes      int64          `json:"rssMemoryBytes"`
	PeakMemoryBytes     int64          `json:"peakMemoryBytes"`
	FragmentationRatio  float64        `json:"fragmentationRatio"`
	MaxMemoryBytes      int64          `json:"maxMemoryBytes"`
	EvictionPolicy      string         `json:"evictionPolicy"`
	OperationsPerSecond int64          `json:"operationsPerSecond"`
	TotalCommands       int64          `json:"totalCommands"`
	TotalConnections    int64          `json:"totalConnections"`
	KeyspaceHits        int64          `json:"keyspaceHits"`
	KeyspaceMisses      int64          `json:"keyspaceMisses"`
	ExpiredKeys         int64          `json:"expiredKeys"`
	EvictedKeys         int64          `json:"evictedKeys"`
	AOFEnabled          bool           `json:"aofEnabled"`
	Commands            []CommandStat  `json:"commands"`
	Keyspaces           []KeyspaceStat `json:"keyspaces"`
}

func (client *Client) Stats(ctx context.Context) (Stats, error) {
	value, err := client.command(ctx, "INFO", "all")
	if err != nil {
		return Stats{}, err
	}
	if value.kind != responseBulk {
		return Stats{}, errors.New("unexpected Redis INFO response")
	}
	return parseStats(value.bulk)
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
		&result.TotalCommands, &result.TotalConnections, &result.KeyspaceHits,
		&result.KeyspaceMisses, &result.ExpiredKeys, &result.EvictedKeys,
	}
	names := []string{
		"uptime_in_seconds", "connected_clients", "blocked_clients", "rejected_connections",
		"used_memory", "used_memory_rss", "used_memory_peak", "maxmemory",
		"instantaneous_ops_per_sec", "total_commands_processed", "total_connections_received",
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
	for name, value := range fields {
		if strings.HasPrefix(name, "cmdstat_") {
			stat, parseErr := parseCommandStat(strings.TrimPrefix(name, "cmdstat_"), value)
			if parseErr != nil {
				return Stats{}, parseErr
			}
			result.Commands = append(result.Commands, stat)
		}
		if strings.HasPrefix(name, "db") {
			stat, parseErr := parseKeyspaceStat(name, value)
			if parseErr != nil {
				return Stats{}, parseErr
			}
			result.Keyspaces = append(result.Keyspaces, stat)
		}
	}
	sort.Slice(result.Commands, func(left, right int) bool { return result.Commands[left].Calls > result.Commands[right].Calls })
	sort.Slice(result.Keyspaces, func(left, right int) bool { return result.Keyspaces[left].Database < result.Keyspaces[right].Database })
	return result, nil
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
