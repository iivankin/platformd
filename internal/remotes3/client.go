package remotes3

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/iivankin/platformd/internal/bucketname"
)

const (
	signatureAlgorithm  = "AWS4-HMAC-SHA256"
	signatureService    = "s3"
	signatureTerminal   = "aws4_request"
	maximumErrorBytes   = 64 << 10
	maximumProbeBytes   = 64 << 10
	defaultProbeTimeout = 30 * time.Second
)

type Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	Prefix          string
	AccessKeyID     string
	SecretAccessKey string
	HTTPClient      *http.Client
	Now             func() time.Time
	Random          io.Reader
}

type Client struct {
	endpoint        *url.URL
	region          string
	bucket          string
	prefix          string
	accessKeyID     string
	secretAccessKey string
	httpClient      *http.Client
	now             func() time.Time
	random          io.Reader
}

type Object struct {
	Key  string
	Size int64
}

type Page struct {
	Objects      []Object
	Continuation string
}

type RemoteError struct {
	StatusCode int
	Code       string
}

func (remote *RemoteError) Error() string {
	if remote.Code == "" {
		return fmt.Sprintf("remote S3 returned HTTP %d", remote.StatusCode)
	}
	return fmt.Sprintf("remote S3 returned HTTP %d (%s)", remote.StatusCode, remote.Code)
}

func New(config Config) (*Client, error) {
	canonical, err := CanonicalConfig(config)
	if err != nil {
		return nil, err
	}
	config = canonical
	endpoint, err := url.Parse(config.Endpoint)
	if err != nil {
		return nil, err
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: defaultProbeTimeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	random := config.Random
	if random == nil {
		random = rand.Reader
	}
	return &Client{
		endpoint: endpoint, region: config.Region, bucket: config.Bucket,
		prefix: normalizePrefix(config.Prefix), accessKeyID: config.AccessKeyID,
		secretAccessKey: config.SecretAccessKey, httpClient: httpClient, now: now, random: random,
	}, nil
}

func CanonicalConfig(config Config) (Config, error) {
	endpoint, err := validateConfig(config)
	if err != nil {
		return Config{}, err
	}
	endpoint.Scheme = strings.ToLower(endpoint.Scheme)
	endpoint.Host = strings.ToLower(endpoint.Host)
	config.Endpoint = strings.TrimSuffix(endpoint.String(), "/")
	config.Prefix = normalizePrefix(config.Prefix)
	return config, nil
}

func (client *Client) Probe(ctx context.Context) error {
	random := make([]byte, 32)
	if _, err := io.ReadFull(client.random, random); err != nil {
		return fmt.Errorf("generate remote S3 probe: %w", err)
	}
	key := client.Key("probes/" + hex.EncodeToString(random[:12]))
	checksum := sha256.Sum256(random)
	if err := client.Put(ctx, key, bytes.NewReader(random), int64(len(random)), hex.EncodeToString(checksum[:])); err != nil {
		return fmt.Errorf("probe remote S3 PUT: %w", err)
	}
	defer func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), defaultProbeTimeout)
		_ = client.Delete(cleanupContext, key)
		cancel()
	}()
	size, err := client.Head(ctx, key)
	if err != nil || size != int64(len(random)) {
		return fmt.Errorf("probe remote S3 HEAD size %d: %w", size, err)
	}
	body, _, err := client.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("probe remote S3 GET: %w", err)
	}
	payload, readErr := io.ReadAll(io.LimitReader(body, maximumProbeBytes+1))
	closeErr := body.Close()
	if readErr != nil || closeErr != nil {
		return fmt.Errorf("read remote S3 probe: %w", errors.Join(readErr, closeErr))
	}
	if !bytes.Equal(payload, random) {
		return errors.New("remote S3 probe GET bytes differ")
	}
	page, err := client.List(ctx, key, "")
	if err != nil {
		return fmt.Errorf("probe remote S3 LIST: %w", err)
	}
	found := false
	for _, object := range page.Objects {
		if object.Key == key && object.Size == int64(len(random)) {
			found = true
			break
		}
	}
	if !found {
		return errors.New("remote S3 probe object is absent from LIST")
	}
	if err := client.Delete(ctx, key); err != nil {
		return fmt.Errorf("probe remote S3 DELETE: %w", err)
	}
	return nil
}

func (client *Client) Key(relative string) string {
	relative = strings.TrimPrefix(relative, "/")
	if client.prefix == "" {
		return relative
	}
	if relative == "" {
		return client.prefix
	}
	return client.prefix + "/" + relative
}

func (client *Client) Put(ctx context.Context, key string, content io.Reader, size int64, payloadSHA256 string) error {
	if err := validateObjectInput(key, size, payloadSHA256); err != nil {
		return err
	}
	request, err := client.request(ctx, http.MethodPut, key, nil, content)
	if err != nil {
		return err
	}
	request.ContentLength = size
	response, err := client.do(request, payloadSHA256)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusNoContent {
		return remoteError(response)
	}
	return drain(response)
}

func (client *Client) Head(ctx context.Context, key string) (int64, error) {
	request, err := client.request(ctx, http.MethodHead, key, nil, nil)
	if err != nil {
		return 0, err
	}
	response, err := client.do(request, emptyPayloadHash())
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, remoteError(response)
	}
	if response.ContentLength < 0 {
		return 0, errors.New("remote S3 HEAD omitted Content-Length")
	}
	return response.ContentLength, nil
}

func (client *Client) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	request, err := client.request(ctx, http.MethodGet, key, nil, nil)
	if err != nil {
		return nil, 0, err
	}
	response, err := client.do(request, emptyPayloadHash())
	if err != nil {
		return nil, 0, err
	}
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		return nil, 0, remoteError(response)
	}
	return response.Body, response.ContentLength, nil
}

func (client *Client) Delete(ctx context.Context, key string) error {
	request, err := client.request(ctx, http.MethodDelete, key, nil, nil)
	if err != nil {
		return err
	}
	response, err := client.do(request, emptyPayloadHash())
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusOK {
		return remoteError(response)
	}
	return drain(response)
}

func (client *Client) List(ctx context.Context, prefix, continuation string) (Page, error) {
	query := url.Values{"list-type": []string{"2"}, "prefix": []string{prefix}}
	if continuation != "" {
		query.Set("continuation-token", continuation)
	}
	request, err := client.request(ctx, http.MethodGet, "", query, nil)
	if err != nil {
		return Page{}, err
	}
	response, err := client.do(request, emptyPayloadHash())
	if err != nil {
		return Page{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Page{}, remoteError(response)
	}
	var result struct {
		IsTruncated           bool
		NextContinuationToken string
		Contents              []struct {
			Key  string
			Size int64
		}
	}
	decoder := xml.NewDecoder(io.LimitReader(response.Body, 16<<20))
	if err := decoder.Decode(&result); err != nil {
		return Page{}, fmt.Errorf("decode remote S3 list: %w", err)
	}
	page := Page{Objects: make([]Object, 0, len(result.Contents))}
	for _, object := range result.Contents {
		if object.Key == "" || object.Size < 0 {
			return Page{}, errors.New("remote S3 list contains an invalid object")
		}
		page.Objects = append(page.Objects, Object{Key: object.Key, Size: object.Size})
	}
	if result.IsTruncated {
		if result.NextContinuationToken == "" || result.NextContinuationToken == continuation {
			return Page{}, errors.New("remote S3 truncated list omitted a new continuation token")
		}
		page.Continuation = result.NextContinuationToken
	}
	return page, nil
}

func (client *Client) request(ctx context.Context, method, key string, query url.Values, body io.Reader) (*http.Request, error) {
	if key != "" {
		if err := validateKey(key); err != nil {
			return nil, err
		}
	}
	value := *client.endpoint
	value.Path = joinURLPath(client.endpoint.Path, client.bucket, key)
	value.RawPath = ""
	value.RawQuery = query.Encode()
	requestBody := body
	if body != nil {
		// net/http closes Request.Body; wrap the caller-owned reader so Put does not take ownership of it.
		requestBody = io.NopCloser(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, value.String(), requestBody)
	if err != nil {
		return nil, err
	}
	request.Host = value.Host
	return request, nil
}

func (client *Client) do(request *http.Request, payloadHash string) (*http.Response, error) {
	timestamp := client.now().UTC()
	request.Header.Set("X-Amz-Date", timestamp.Format("20060102T150405Z"))
	request.Header.Set("X-Amz-Content-Sha256", payloadHash)
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonical := strings.Join([]string{
		request.Method,
		canonicalURI(request.URL),
		canonicalQuery(request.URL.Query()),
		"host:" + request.Host + "\n" +
			"x-amz-content-sha256:" + payloadHash + "\n" +
			"x-amz-date:" + timestamp.Format("20060102T150405Z") + "\n",
		signedHeaders,
		payloadHash,
	}, "\n")
	date := timestamp.Format("20060102")
	scope := date + "/" + client.region + "/" + signatureService + "/" + signatureTerminal
	canonicalHash := sha256.Sum256([]byte(canonical))
	stringToSign := strings.Join([]string{
		signatureAlgorithm,
		timestamp.Format("20060102T150405Z"),
		scope,
		hex.EncodeToString(canonicalHash[:]),
	}, "\n")
	signature := hex.EncodeToString(hmacSHA256(signingKey(client.secretAccessKey, date, client.region), stringToSign))
	request.Header.Set("Authorization", signatureAlgorithm+" Credential="+client.accessKeyID+"/"+scope+
		",SignedHeaders="+signedHeaders+",Signature="+signature)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("send remote S3 request: %w", err)
	}
	return response, nil
}

func validateConfig(config Config) (*url.URL, error) {
	endpoint, err := url.Parse(config.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil ||
		endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Opaque != "" {
		return nil, errors.New("remote S3 endpoint must be an absolute HTTPS URL without credentials, query, or fragment")
	}
	if endpoint.Path != "" && endpoint.Path != "/" {
		clean := normalizePrefix(endpoint.Path)
		if clean == "" || hasUnsafePathComponent(clean) {
			return nil, errors.New("remote S3 endpoint path is invalid")
		}
		endpoint.Path = "/" + clean
	} else {
		endpoint.Path = ""
	}
	if config.Region == "" || len(config.Region) > 64 || !simpleIdentifier(config.Region) {
		return nil, errors.New("remote S3 region is invalid")
	}
	if err := bucketname.Validate(config.Bucket); err != nil {
		return nil, err
	}
	if config.AccessKeyID == "" || len(config.AccessKeyID) > 1024 || strings.ContainsAny(config.AccessKeyID, "\r\n\x00") {
		return nil, errors.New("remote S3 access key ID is invalid")
	}
	if config.SecretAccessKey == "" || len(config.SecretAccessKey) > 4096 || strings.ContainsRune(config.SecretAccessKey, '\x00') {
		return nil, errors.New("remote S3 secret access key is invalid")
	}
	prefix := normalizePrefix(config.Prefix)
	if (config.Prefix != "" && prefix == "") || (prefix != "" && hasUnsafePathComponent(prefix)) || len(prefix) > 768 {
		return nil, errors.New("remote S3 prefix is invalid")
	}
	return endpoint, nil
}

func validateObjectInput(key string, size int64, payloadSHA256 string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if size < 0 {
		return errors.New("remote S3 object size cannot be negative")
	}
	if len(payloadSHA256) != sha256.Size*2 {
		return errors.New("remote S3 payload SHA-256 is invalid")
	}
	if _, err := hex.DecodeString(payloadSHA256); err != nil {
		return errors.New("remote S3 payload SHA-256 is invalid")
	}
	return nil
}

func validateKey(key string) error {
	if key == "" || len(key) > 1024 || strings.HasPrefix(key, "/") || strings.ContainsAny(key, "\r\n\x00") ||
		hasUnsafePathComponent(key) {
		return errors.New("remote S3 object key is invalid")
	}
	return nil
}

func hasUnsafePathComponent(value string) bool {
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			return true
		}
	}
	return false
}

func normalizePrefix(value string) string {
	return strings.Trim(value, "/")
}

func simpleIdentifier(value string) bool {
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return false
		}
	}
	return true
}

func joinURLPath(base, bucket, key string) string {
	components := []string{strings.TrimSuffix(base, "/"), bucket}
	if key != "" {
		components = append(components, strings.Split(key, "/")...)
	}
	return strings.Join(components, "/")
}

func canonicalURI(value *url.URL) string {
	path := value.EscapedPath()
	if path == "" {
		path = "/"
	}
	decoded, err := url.PathUnescape(path)
	if err != nil {
		panic("request URL was constructed from validated components")
	}
	return awsEncode(decoded, false)
}

func canonicalQuery(values url.Values) string {
	type pair struct{ name, value string }
	pairs := make([]pair, 0)
	for name, entries := range values {
		if len(entries) == 0 {
			entries = []string{""}
		}
		for _, value := range entries {
			pairs = append(pairs, pair{name: awsEncode(name, true), value: awsEncode(value, true)})
		}
	}
	sort.Slice(pairs, func(left, right int) bool {
		if pairs[left].name == pairs[right].name {
			return pairs[left].value < pairs[right].value
		}
		return pairs[left].name < pairs[right].name
	})
	encoded := make([]string, len(pairs))
	for index, item := range pairs {
		encoded[index] = item.name + "=" + item.value
	}
	return strings.Join(encoded, "&")
}

func awsEncode(value string, encodeSlash bool) string {
	const hexadecimal = "0123456789ABCDEF"
	var builder strings.Builder
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' || character == '~' ||
			character == '/' && !encodeSlash {
			builder.WriteByte(character)
			continue
		}
		builder.WriteByte('%')
		builder.WriteByte(hexadecimal[character>>4])
		builder.WriteByte(hexadecimal[character&15])
	}
	return builder.String()
}

func emptyPayloadHash() string {
	value := sha256.Sum256(nil)
	return hex.EncodeToString(value[:])
}

func signingKey(secret, date, region string) []byte {
	dateKey := hmacSHA256([]byte("AWS4"+secret), date)
	regionKey := hmacSHA256(dateKey, region)
	serviceKey := hmacSHA256(regionKey, signatureService)
	return hmacSHA256(serviceKey, signatureTerminal)
}

func hmacSHA256(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

func remoteError(response *http.Response) error {
	payload, _ := io.ReadAll(io.LimitReader(response.Body, maximumErrorBytes))
	var envelope struct {
		Code string
	}
	_ = xml.Unmarshal(payload, &envelope)
	return &RemoteError{StatusCode: response.StatusCode, Code: envelope.Code}
}

func drain(response *http.Response) error {
	_, err := io.Copy(io.Discard, io.LimitReader(response.Body, maximumErrorBytes))
	return err
}
