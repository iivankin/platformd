package hostagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/iivankin/platformd/internal/hostconn"
	"github.com/iivankin/platformd/internal/state"
)

type JoinInput struct {
	URL        string
	Token      string
	Name       string
	PublicIPv4 string
	HTTPClient *http.Client
}

type JoinResult struct {
	HostID    string
	HostToken string
	ParentURL string `json:"-"`
	Name      string
}

func Join(ctx context.Context, input JoinInput) (JoinResult, error) {
	if input.URL == "" || input.Token == "" {
		return JoinResult{}, fmt.Errorf("join URL and token are required")
	}
	parentURL, err := NormalizeParentURL(input.URL)
	if err != nil {
		return JoinResult{}, err
	}
	publicIPv4 := input.PublicIPv4
	if publicIPv4 == "" {
		detected, err := DetectPublicIPv4(ctx)
		if err != nil {
			return JoinResult{}, err
		}
		publicIPv4 = detected
	}
	if err := state.ValidatePublicIPv4(publicIPv4); err != nil {
		return JoinResult{}, err
	}
	body, err := json.Marshal(map[string]string{
		"token": input.Token, "name": input.Name, "publicIpv4": publicIPv4,
	})
	if err != nil {
		return JoinResult{}, err
	}
	endpoint := parentURL + hostconn.JoinPath
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return JoinResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	client := input.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return JoinResult{}, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<16))
	if err != nil {
		return JoinResult{}, err
	}
	if response.StatusCode != http.StatusCreated {
		return JoinResult{}, fmt.Errorf("join failed: %s", strings.TrimSpace(string(payload)))
	}
	var result JoinResult
	if err := json.Unmarshal(payload, &result); err != nil {
		return JoinResult{}, err
	}
	if result.HostID == "" || result.HostToken == "" {
		return JoinResult{}, fmt.Errorf("join response is incomplete")
	}
	result.ParentURL = parentURL
	return result, nil
}

func DetectPublicIPv4(ctx context.Context) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://1.1.1.1/cdn-cgi/trace", nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("detect public IPv4: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(body), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok && key == "ip" {
			if err := state.ValidatePublicIPv4(value); err != nil {
				return "", fmt.Errorf("detected address %q is not a public IPv4: %w", value, err)
			}
			return value, nil
		}
	}
	return "", fmt.Errorf("public IPv4 was not present in the detection response")
}

func DefaultName() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "worker"
	}
	name = strings.ToLower(strings.TrimSpace(name))
	if index := strings.IndexByte(name, '.'); index > 0 {
		name = name[:index]
	}
	return name
}
