package cloudflaremesh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

type apiClient struct {
	client  *http.Client
	baseURL string
}

type meshNode struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (client *apiClient) findOrCreateNode(ctx context.Context, accountID, token, name string) (meshNode, error) {
	var nodes []meshNode
	path := "/accounts/" + url.PathEscape(accountID) + "/warp_connector?per_page=100"
	if err := client.request(ctx, http.MethodGet, path, token, nil, &nodes); err != nil {
		return meshNode{}, fmt.Errorf("list Cloudflare Mesh nodes: %w", err)
	}
	for _, node := range nodes {
		if node.Name == name && node.ID != "" {
			return node, nil
		}
	}
	var created meshNode
	if err := client.request(ctx, http.MethodPost, "/accounts/"+url.PathEscape(accountID)+"/warp_connector", token, map[string]any{
		"name": name,
		"ha":   true,
	}, &created); err != nil {
		return meshNode{}, fmt.Errorf("create Cloudflare Mesh node: %w", err)
	}
	if created.ID == "" {
		return meshNode{}, fmt.Errorf("create Cloudflare Mesh node: response has no node ID")
	}
	return created, nil
}

func (client *apiClient) node(ctx context.Context, accountID, token, nodeID string) (meshNode, error) {
	var node meshNode
	path := "/accounts/" + url.PathEscape(accountID) + "/warp_connector/" + url.PathEscape(nodeID)
	if err := client.request(ctx, http.MethodGet, path, token, nil, &node); err != nil {
		return meshNode{}, err
	}
	if node.ID == "" {
		return meshNode{}, fmt.Errorf("Cloudflare Mesh response has no node ID")
	}
	return node, nil
}

func (client *apiClient) nodeToken(ctx context.Context, accountID, token, nodeID string) ([]byte, error) {
	var value string
	path := "/accounts/" + url.PathEscape(accountID) + "/warp_connector/" + url.PathEscape(nodeID) + "/token"
	if err := client.request(ctx, http.MethodGet, path, token, nil, &value); err != nil {
		return nil, fmt.Errorf("get Cloudflare Mesh node token: %w", err)
	}
	if value == "" {
		return nil, fmt.Errorf("get Cloudflare Mesh node token: response is empty")
	}
	return []byte(value), nil
}

func (client *apiClient) request(ctx context.Context, method, path, token string, body, destination any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.client.Do(request)
	if err != nil {
		return fmt.Errorf("Cloudflare API request: %w", err)
	}
	defer response.Body.Close()
	var envelope struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&envelope); err != nil {
		return fmt.Errorf("decode Cloudflare API response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !envelope.Success {
		message := http.StatusText(response.StatusCode)
		if len(envelope.Errors) > 0 && envelope.Errors[0].Message != "" {
			message = envelope.Errors[0].Message
		}
		return fmt.Errorf("Cloudflare API %s %s returned %d: %s", method, path, response.StatusCode, message)
	}
	if destination != nil && len(envelope.Result) != 0 {
		if err := json.Unmarshal(envelope.Result, destination); err != nil {
			return fmt.Errorf("decode Cloudflare API result: %w", err)
		}
	}
	return nil
}
