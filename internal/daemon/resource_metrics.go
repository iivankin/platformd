package daemon

import (
	"sync/atomic"

	"github.com/iivankin/platformd/internal/firewall"
	"github.com/iivankin/platformd/internal/resourcemetrics"
	"github.com/iivankin/platformd/internal/trafficmetrics"
)

type publicNetworkReader struct {
	proxy    *trafficmetrics.Registry
	firewall *firewall.Manager
	tcp      atomic.Pointer[tcpTrafficSampler]
}

type tcpTrafficSampler struct {
	sample func()
}

func (reader *publicNetworkReader) setTCPSampler(sample func()) {
	if sample != nil {
		reader.tcp.Store(&tcpTrafficSampler{sample: sample})
	}
}

// PublicNetworkCounters combines disjoint paths: host-terminated public
// proxies and directly forwarded container traffic. It performs one nftables
// object dump for the whole installation, never one container/netns query.
func (reader *publicNetworkReader) PublicNetworkCounters() (resourcemetrics.PublicNetworkSnapshot, error) {
	if sampler := reader.tcp.Load(); sampler != nil {
		sampler.sample()
	}
	result := reader.proxy.Collect()
	direct, err := reader.firewall.PublicTraffic()
	if err != nil {
		return resourcemetrics.PublicNetworkSnapshot{Counters: result}, err
	}
	for serviceID, counters := range direct {
		current := result[serviceID]
		current.IngressBytes += counters.IngressBytes
		current.EgressBytes += counters.EgressBytes
		result[serviceID] = current
	}
	return resourcemetrics.PublicNetworkSnapshot{Counters: result, Complete: true}, nil
}
