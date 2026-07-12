package internaldns

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const (
	maxDNSMessageBytes = 4096
	maxCacheEntries    = 512
	maxCacheTTL        = 30 * time.Second
	upstreamTimeout    = 3 * time.Second
)

type ForwardCache struct {
	upstreams  []netip.AddrPort
	semaphore  chan struct{}
	next       atomic.Uint64
	mu         sync.Mutex
	cache      map[string]cacheEntry
	maxEntries int
}

type cacheEntry struct {
	packet    []byte
	expiresAt time.Time
}

func NewForwardCache(upstreams []netip.AddrPort, disallowed []netip.Addr) (*ForwardCache, error) {
	if len(upstreams) == 0 || len(upstreams) > 3 {
		return nil, errors.New("internal DNS requires between one and three upstream resolvers")
	}
	blocked := make(map[netip.Addr]struct{}, len(disallowed))
	for _, address := range disallowed {
		blocked[address.Unmap()] = struct{}{}
	}
	canonical := make([]netip.AddrPort, 0, len(upstreams))
	seen := make(map[netip.AddrPort]struct{}, len(upstreams))
	for _, upstream := range upstreams {
		address := upstream.Addr().Unmap()
		if !address.IsValid() || !address.Is4() {
			return nil, fmt.Errorf("upstream DNS address %s is not IPv4", upstream)
		}
		if _, exists := blocked[address]; exists {
			return nil, fmt.Errorf("upstream DNS address %s conflicts with a platform listener", address)
		}
		if upstream.Port() == 0 {
			upstream = netip.AddrPortFrom(address, 53)
		} else {
			upstream = netip.AddrPortFrom(address, upstream.Port())
		}
		if _, exists := seen[upstream]; exists {
			continue
		}
		seen[upstream] = struct{}{}
		canonical = append(canonical, upstream)
	}
	if len(canonical) == 0 {
		return nil, errors.New("internal DNS has no unique upstream resolvers")
	}
	return &ForwardCache{
		upstreams: canonical, semaphore: make(chan struct{}, 64),
		cache: make(map[string]cacheEntry), maxEntries: maxCacheEntries,
	}, nil
}

func (forwarder *ForwardCache) Resolve(ctx context.Context, query []byte) ([]byte, error) {
	if len(query) < 12 || len(query) > maxDNSMessageBytes {
		return nil, errors.New("forwarded DNS query size is outside supported bounds")
	}
	keyBytes := append([]byte(nil), query...)
	keyBytes[0], keyBytes[1] = 0, 0
	key := string(keyBytes)
	if cached, found := forwarder.load(key, query[:2]); found {
		return cached, nil
	}
	select {
	case forwarder.semaphore <- struct{}{}:
		defer func() { <-forwarder.semaphore }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	resolveContext, cancel := context.WithTimeout(ctx, upstreamTimeout)
	defer cancel()
	start := int(forwarder.next.Add(1)-1) % len(forwarder.upstreams)
	var failures []error
	for offset := range len(forwarder.upstreams) {
		upstream := forwarder.upstreams[(start+offset)%len(forwarder.upstreams)]
		response, err := exchangeDNS(resolveContext, upstream, query)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", upstream, err))
			continue
		}
		if ttl := responseTTL(response); ttl > 0 {
			forwarder.store(key, response, ttl)
		}
		return response, nil
	}
	return nil, fmt.Errorf("all upstream DNS resolvers failed: %w", errors.Join(failures...))
}

func (forwarder *ForwardCache) load(key string, identifier []byte) ([]byte, bool) {
	forwarder.mu.Lock()
	defer forwarder.mu.Unlock()
	entry, found := forwarder.cache[key]
	if !found || !time.Now().Before(entry.expiresAt) {
		delete(forwarder.cache, key)
		return nil, false
	}
	packet := append([]byte(nil), entry.packet...)
	copy(packet[:2], identifier)
	return packet, true
}

func (forwarder *ForwardCache) store(key string, packet []byte, ttl time.Duration) {
	stored := append([]byte(nil), packet...)
	stored[0], stored[1] = 0, 0
	forwarder.mu.Lock()
	defer forwarder.mu.Unlock()
	now := time.Now()
	for cacheKey, entry := range forwarder.cache {
		if !now.Before(entry.expiresAt) {
			delete(forwarder.cache, cacheKey)
		}
	}
	if len(forwarder.cache) >= forwarder.maxEntries {
		for cacheKey := range forwarder.cache {
			delete(forwarder.cache, cacheKey)
			break
		}
	}
	forwarder.cache[key] = cacheEntry{packet: stored, expiresAt: now.Add(min(ttl, maxCacheTTL))}
}

func exchangeDNS(ctx context.Context, upstream netip.AddrPort, query []byte) ([]byte, error) {
	response, err := exchangePacket(ctx, "udp", upstream, query)
	if err != nil {
		return nil, err
	}
	if len(response) >= 4 && response[2]&0x02 != 0 {
		return exchangePacket(ctx, "tcp", upstream, query)
	}
	return response, nil
}

func exchangePacket(ctx context.Context, network string, upstream netip.AddrPort, query []byte) ([]byte, error) {
	connection, err := (&net.Dialer{}).DialContext(ctx, network, upstream.String())
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	deadline := time.Now().Add(upstreamTimeout)
	if contextDeadline, present := ctx.Deadline(); present && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return nil, err
	}
	if network == "tcp" {
		var size [2]byte
		binary.BigEndian.PutUint16(size[:], uint16(len(query)))
		if err := writeAll(connection, append(size[:], query...)); err != nil {
			return nil, err
		}
		if _, err := io.ReadFull(connection, size[:]); err != nil {
			return nil, err
		}
		length := int(binary.BigEndian.Uint16(size[:]))
		if length < 12 || length > maxDNSMessageBytes {
			return nil, fmt.Errorf("upstream TCP DNS response size %d is outside bounds", length)
		}
		response := make([]byte, length)
		if _, err := io.ReadFull(connection, response); err != nil {
			return nil, err
		}
		return validateUpstreamResponse(query, response)
	}
	if _, err := connection.Write(query); err != nil {
		return nil, err
	}
	buffer := make([]byte, maxDNSMessageBytes)
	length, err := connection.Read(buffer)
	if err != nil {
		return nil, err
	}
	return validateUpstreamResponse(query, buffer[:length])
}

func validateUpstreamResponse(query, response []byte) ([]byte, error) {
	if len(response) < 12 || response[2]&0x80 == 0 || response[0] != query[0] || response[1] != query[1] {
		return nil, errors.New("upstream returned an invalid DNS response")
	}
	return append([]byte(nil), response...), nil
}

func writeAll(writer io.Writer, value []byte) error {
	for len(value) > 0 {
		written, err := writer.Write(value)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		value = value[written:]
	}
	return nil
}

func responseTTL(packet []byte) time.Duration {
	var parser dnsmessage.Parser
	if _, err := parser.Start(packet); err != nil {
		return 0
	}
	if err := parser.SkipAllQuestions(); err != nil {
		return 0
	}
	var minimum uint32
	found := false
	for {
		header, err := parser.AnswerHeader()
		if errors.Is(err, dnsmessage.ErrSectionDone) {
			break
		}
		if err != nil {
			return 0
		}
		if header.TTL == 0 {
			return 0
		}
		if !found || header.TTL < minimum {
			minimum = header.TTL
		}
		found = true
		if err := parser.SkipAnswer(); err != nil {
			return 0
		}
	}
	if !found {
		return 0
	}
	return time.Duration(minimum) * time.Second
}
