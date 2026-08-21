package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"sync/atomic"
	"time"
)

const (
	botRangeRefreshInterval = 24 * time.Hour
	maximumBotRangeBytes    = 1 << 20
)

type botRangeSource struct {
	url      string
	botNames []string
}

var officialBotRangeSources = []botRangeSource{
	{url: "https://openai.com/gptbot.json", botNames: []string{"GPTBot"}},
	{url: "https://openai.com/searchbot.json", botNames: []string{"OAI-SearchBot"}},
	{url: "https://openai.com/chatgpt-user.json", botNames: []string{"ChatGPT-User"}},
	{
		url:      "https://claude.com/crawling/bots.json",
		botNames: []string{"ClaudeBot", "Claude-SearchBot", "Claude-User"},
	},
	{url: "https://www.perplexity.com/perplexitybot.json", botNames: []string{"PerplexityBot"}},
	{url: "https://www.perplexity.com/perplexity-user.json", botNames: []string{"Perplexity-User"}},
}

type botRangeSnapshot struct {
	byName map[string][]netip.Prefix
}

type BotRangeVerifier struct {
	client  Transport
	sources []botRangeSource
	current atomic.Pointer[botRangeSnapshot]
}

func NewBotRangeVerifier(client Transport) *BotRangeVerifier {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return newBotRangeVerifier(client, officialBotRangeSources)
}

func newBotRangeVerifier(client Transport, sources []botRangeSource) *BotRangeVerifier {
	return &BotRangeVerifier{client: client, sources: sources}
}

func (verifier *BotRangeVerifier) Verify(botName, address string) bool {
	parsed, err := netip.ParseAddr(address)
	if err != nil {
		return false
	}
	snapshot := verifier.current.Load()
	if snapshot == nil {
		return false
	}
	parsed = parsed.Unmap()
	for _, prefix := range snapshot.byName[botName] {
		if prefix.Contains(parsed) {
			return true
		}
	}
	return false
}

func (verifier *BotRangeVerifier) Run(ctx context.Context, onError func(error)) {
	report := func(err error) {
		if err != nil && onError != nil {
			onError(err)
		}
	}
	report(verifier.refresh(ctx))
	ticker := time.NewTicker(botRangeRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			report(verifier.refresh(ctx))
		}
	}
}

func (verifier *BotRangeVerifier) refresh(ctx context.Context) error {
	next := make(map[string][]netip.Prefix)
	if current := verifier.current.Load(); current != nil {
		for name, prefixes := range current.byName {
			next[name] = prefixes
		}
	}
	loaded := 0
	var refreshErrors []error
	for _, source := range verifier.sources {
		prefixes, err := verifier.fetch(ctx, source.url)
		if err != nil {
			refreshErrors = append(refreshErrors, fmt.Errorf("%s: %w", source.url, err))
			continue
		}
		for _, name := range source.botNames {
			next[name] = prefixes
		}
		loaded++
	}
	if loaded != 0 {
		verifier.current.Store(&botRangeSnapshot{byName: next})
	}
	return errors.Join(refreshErrors...)
}

func (verifier *BotRangeVerifier) fetch(ctx context.Context, sourceURL string) ([]netip.Prefix, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := verifier.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", response.StatusCode)
	}
	var document struct {
		Prefixes []struct {
			IPv4 string `json:"ipv4Prefix"`
			IPv6 string `json:"ipv6Prefix"`
		} `json:"prefixes"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maximumBotRangeBytes)).Decode(&document); err != nil {
		return nil, fmt.Errorf("decode ranges: %w", err)
	}
	prefixes := make([]netip.Prefix, 0, len(document.Prefixes))
	for _, candidate := range document.Prefixes {
		for _, value := range []string{candidate.IPv4, candidate.IPv6} {
			if value == "" {
				continue
			}
			prefix, err := netip.ParsePrefix(value)
			if err != nil {
				return nil, fmt.Errorf("parse prefix %q: %w", value, err)
			}
			prefixes = append(prefixes, prefix.Masked())
		}
	}
	if len(prefixes) == 0 {
		return nil, errors.New("range list is empty")
	}
	return prefixes, nil
}
