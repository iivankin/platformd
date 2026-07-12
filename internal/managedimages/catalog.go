package managedimages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.podman.io/image/v5/docker/reference"
)

const DefaultPageSize = 50
const MaximumPageSize = 100
const maximumResponseBytes = 2 << 20

var ErrInvalidQuery = errors.New("invalid managed image tag query")

type Engine string

const (
	PostgreSQL Engine = "postgres"
	Redis      Engine = "redis"
)

type Platform struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
	Digest       string `json:"digest"`
	SizeBytes    int64  `json:"sizeBytes"`
}

type Tag struct {
	Name        string     `json:"name"`
	LastUpdated time.Time  `json:"lastUpdated"`
	Platforms   []Platform `json:"platforms"`
}

type Page struct {
	Tags               []Tag `json:"tags"`
	Page               int   `json:"page"`
	PageSize           int   `json:"pageSize"`
	Total              int   `json:"total"`
	NextPage           int   `json:"nextPage,omitempty"`
	PreviousPage       int   `json:"previousPage,omitempty"`
	RateLimitRemaining int   `json:"rateLimitRemaining,omitempty"`
	RateLimitReset     int64 `json:"rateLimitReset,omitempty"`
}

type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
}

func New(baseURL string, httpClient *http.Client) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("managed image catalog base URL is invalid")
	}
	if httpClient == nil {
		return nil, errors.New("managed image catalog HTTP client is required")
	}
	return &Client{baseURL: parsed, httpClient: httpClient}, nil
}

func (client *Client) List(ctx context.Context, engine Engine, page, pageSize int) (Page, error) {
	repository, err := Repository(engine)
	if err != nil {
		return Page{}, err
	}
	if page == 0 {
		page = 1
	}
	if pageSize == 0 {
		pageSize = DefaultPageSize
	}
	if page < 1 || pageSize < 1 || pageSize > MaximumPageSize {
		return Page{}, fmt.Errorf("%w: page must be positive and pageSize must be 1..100", ErrInvalidQuery)
	}
	requestURL := client.baseURL.ResolveReference(&url.URL{
		Path: "/v2/namespaces/library/repositories/" + repository + "/tags",
	})
	query := requestURL.Query()
	query.Set("page", strconv.Itoa(page))
	query.Set("page_size", strconv.Itoa(pageSize))
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return Page{}, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return Page{}, fmt.Errorf("list official %s image tags: %w", engine, err)
	}
	defer response.Body.Close()
	value, err := io.ReadAll(io.LimitReader(response.Body, maximumResponseBytes+1))
	if err != nil {
		return Page{}, fmt.Errorf("read Docker Hub tag response: %w", err)
	}
	if len(value) > maximumResponseBytes {
		return Page{}, errors.New("Docker Hub tag response exceeds 2 MiB")
	}
	if response.StatusCode != http.StatusOK {
		return Page{}, fmt.Errorf("Docker Hub tag response status %d", response.StatusCode)
	}
	var payload hubPage
	if err := json.Unmarshal(value, &payload); err != nil {
		return Page{}, fmt.Errorf("decode Docker Hub tag response: %w", err)
	}
	if payload.Count < 0 {
		return Page{}, errors.New("Docker Hub tag response has a negative count")
	}
	result := Page{Tags: make([]Tag, 0, len(payload.Results)), Page: page, PageSize: pageSize, Total: payload.Count}
	for _, remote := range payload.Results {
		if !validRemoteTag(repository, remote.Name) {
			continue
		}
		updated, err := time.Parse(time.RFC3339Nano, remote.LastUpdated)
		if err != nil {
			continue
		}
		tag := Tag{Name: remote.Name, LastUpdated: updated, Platforms: make([]Platform, 0, len(remote.Images))}
		for _, image := range remote.Images {
			if image.Architecture == "" || image.OS == "" || image.Digest == "" || image.Size < 0 {
				continue
			}
			tag.Platforms = append(tag.Platforms, Platform{
				Architecture: image.Architecture, OS: image.OS, Digest: image.Digest, SizeBytes: image.Size,
			})
		}
		result.Tags = append(result.Tags, tag)
	}
	if payload.Next != nil && *payload.Next != "" {
		result.NextPage = page + 1
	}
	if payload.Previous != nil && *payload.Previous != "" && page > 1 {
		result.PreviousPage = page - 1
	}
	result.RateLimitRemaining, _ = strconv.Atoi(response.Header.Get("X-RateLimit-Remaining"))
	result.RateLimitReset, _ = strconv.ParseInt(response.Header.Get("X-RateLimit-Reset"), 10, 64)
	return result, nil
}

func Repository(engine Engine) (string, error) {
	switch engine {
	case PostgreSQL:
		return "postgres", nil
	case Redis:
		return "redis", nil
	default:
		return "", fmt.Errorf("%w: engine must be postgres or redis", ErrInvalidQuery)
	}
}

func Reference(engine Engine, tag string) (string, error) {
	repository, err := Repository(engine)
	if err != nil {
		return "", err
	}
	named, err := reference.ParseNormalizedNamed("docker.io/library/" + repository)
	if err != nil {
		return "", err
	}
	tagged, err := reference.WithTag(named, strings.TrimSpace(tag))
	if err != nil {
		return "", fmt.Errorf("%w: invalid image tag", ErrInvalidQuery)
	}
	return tagged.String(), nil
}

func Filter(page Page, search string) (Page, error) {
	search = strings.TrimSpace(search)
	if len(search) > 128 || strings.ContainsRune(search, '\x00') {
		return Page{}, fmt.Errorf("%w: search must contain at most 128 bytes without NUL", ErrInvalidQuery)
	}
	if search == "" {
		return page, nil
	}
	needle := strings.ToLower(search)
	filtered := page
	filtered.Tags = make([]Tag, 0, len(page.Tags))
	for _, tag := range page.Tags {
		if strings.Contains(strings.ToLower(tag.Name), needle) {
			filtered.Tags = append(filtered.Tags, tag)
		}
	}
	return filtered, nil
}

func validRemoteTag(repository, tag string) bool {
	named, err := reference.ParseNormalizedNamed("docker.io/library/" + repository)
	if err != nil {
		return false
	}
	_, err = reference.WithTag(named, tag)
	return err == nil
}

type hubPage struct {
	Count    int      `json:"count"`
	Next     *string  `json:"next"`
	Previous *string  `json:"previous"`
	Results  []hubTag `json:"results"`
}

type hubTag struct {
	Name        string     `json:"name"`
	LastUpdated string     `json:"last_updated"`
	Images      []hubImage `json:"images"`
}

type hubImage struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
	Digest       string `json:"digest"`
	Size         int64  `json:"size"`
}
