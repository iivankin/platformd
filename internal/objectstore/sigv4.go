package objectstore

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	Region             = "us-east-1"
	signatureAlgorithm = "AWS4-HMAC-SHA256"
	signatureService   = "s3"
	signatureTerminal  = "aws4_request"
	MaximumPresignAge  = 7 * 24 * time.Hour
	defaultClockSkew   = 15 * time.Minute
)

var (
	ErrInvalidSignature = errors.New("invalid S3 SigV4 signature")
	ErrExpiredSignature = errors.New("S3 SigV4 signature is expired")
)

type CredentialResolver func(context.Context, string) (Credential, error)

type SigV4Config struct {
	Resolve   CredentialResolver
	Now       func() time.Time
	ClockSkew time.Duration
}

type SigV4Verifier struct {
	resolve   CredentialResolver
	now       func() time.Time
	clockSkew time.Duration
}

func NewSigV4Verifier(config SigV4Config) (*SigV4Verifier, error) {
	if config.Resolve == nil {
		return nil, errors.New("SigV4 credential resolver is required")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	clockSkew := config.ClockSkew
	if clockSkew == 0 {
		clockSkew = defaultClockSkew
	}
	if clockSkew < 0 {
		return nil, errors.New("SigV4 clock skew cannot be negative")
	}
	return &SigV4Verifier{resolve: config.Resolve, now: now, clockSkew: clockSkew}, nil
}

func (verifier *SigV4Verifier) Verify(ctx context.Context, request *http.Request) (Credential, error) {
	if request == nil {
		return Credential{}, ErrInvalidSignature
	}
	if request.URL.Query().Get("X-Amz-Algorithm") != "" {
		return verifier.verifyPresigned(ctx, request)
	}
	return verifier.verifyHeader(ctx, request)
}

type signatureParts struct {
	AccessKey     string
	Date          string
	Region        string
	SignedHeaders string
	Signature     string
	Timestamp     time.Time
	PayloadHash   string
}

func (verifier *SigV4Verifier) verifyHeader(ctx context.Context, request *http.Request) (Credential, error) {
	if len(request.Header.Values("Authorization")) != 1 || len(request.Header.Values("X-Amz-Date")) != 1 || len(request.Header.Values("X-Amz-Content-Sha256")) != 1 {
		return Credential{}, ErrInvalidSignature
	}
	parts, err := parseAuthorization(request.Header.Get("Authorization"))
	if err != nil {
		return Credential{}, err
	}
	timestamp, err := time.Parse("20060102T150405Z", request.Header.Get("X-Amz-Date"))
	if err != nil {
		return Credential{}, ErrInvalidSignature
	}
	parts.Timestamp = timestamp
	parts.PayloadHash = request.Header.Get("X-Amz-Content-Sha256")
	if !validPayloadHash(parts.PayloadHash) || (request.Method == http.MethodPut && parts.PayloadHash == "UNSIGNED-PAYLOAD") {
		return Credential{}, ErrInvalidSignature
	}
	if err := verifier.validateScopeAndTime(parts, 0); err != nil {
		return Credential{}, err
	}
	credential, err := verifier.resolve(ctx, parts.AccessKey)
	if err != nil {
		return Credential{}, ErrInvalidSignature
	}
	canonical, err := canonicalRequest(request, parts.SignedHeaders, parts.PayloadHash, false)
	if err != nil {
		return Credential{}, err
	}
	if !signatureMatches(parts, credential.Secret, canonical) {
		return Credential{}, ErrInvalidSignature
	}
	return credential, nil
}

func (verifier *SigV4Verifier) verifyPresigned(ctx context.Context, request *http.Request) (Credential, error) {
	query := request.URL.Query()
	for _, name := range []string{"X-Amz-Algorithm", "X-Amz-Credential", "X-Amz-Date", "X-Amz-Expires", "X-Amz-Signature", "X-Amz-SignedHeaders"} {
		if len(query[name]) != 1 {
			return Credential{}, ErrInvalidSignature
		}
	}
	if query.Get("X-Amz-Algorithm") != signatureAlgorithm || query.Get("X-Amz-Security-Token") != "" {
		return Credential{}, ErrInvalidSignature
	}
	accessKey, date, region, err := parseCredentialScope(query.Get("X-Amz-Credential"))
	if err != nil {
		return Credential{}, err
	}
	timestamp, err := time.Parse("20060102T150405Z", query.Get("X-Amz-Date"))
	if err != nil {
		return Credential{}, ErrInvalidSignature
	}
	expiresSeconds, err := strconv.ParseInt(query.Get("X-Amz-Expires"), 10, 64)
	if err != nil || expiresSeconds < 1 || expiresSeconds > int64(MaximumPresignAge/time.Second) {
		return Credential{}, ErrInvalidSignature
	}
	parts := signatureParts{
		AccessKey: accessKey, Date: date, Region: region, SignedHeaders: query.Get("X-Amz-SignedHeaders"),
		Signature: query.Get("X-Amz-Signature"), Timestamp: timestamp, PayloadHash: "UNSIGNED-PAYLOAD",
	}
	if err := verifier.validateScopeAndTime(parts, time.Duration(expiresSeconds)*time.Second); err != nil {
		return Credential{}, err
	}
	credential, err := verifier.resolve(ctx, accessKey)
	if err != nil {
		return Credential{}, ErrInvalidSignature
	}
	canonical, err := canonicalRequest(request, parts.SignedHeaders, parts.PayloadHash, true)
	if err != nil {
		return Credential{}, err
	}
	if !signatureMatches(parts, credential.Secret, canonical) {
		return Credential{}, ErrInvalidSignature
	}
	return credential, nil
}

func (verifier *SigV4Verifier) validateScopeAndTime(parts signatureParts, expires time.Duration) error {
	if parts.Region != Region || parts.Date != parts.Timestamp.UTC().Format("20060102") {
		return ErrInvalidSignature
	}
	now := verifier.now().UTC()
	if expires == 0 {
		if now.Before(parts.Timestamp.Add(-verifier.clockSkew)) || now.After(parts.Timestamp.Add(verifier.clockSkew)) {
			return ErrExpiredSignature
		}
		return nil
	}
	if now.Before(parts.Timestamp.Add(-verifier.clockSkew)) || now.After(parts.Timestamp.Add(expires)) {
		return ErrExpiredSignature
	}
	return nil
}

func parseAuthorization(value string) (signatureParts, error) {
	if len(value) > 4096 || !strings.HasPrefix(value, signatureAlgorithm+" ") {
		return signatureParts{}, ErrInvalidSignature
	}
	fields := make(map[string]string, 3)
	for _, item := range strings.Split(strings.TrimPrefix(value, signatureAlgorithm+" "), ",") {
		name, content, ok := strings.Cut(strings.TrimSpace(item), "=")
		if !ok || fields[name] != "" {
			return signatureParts{}, ErrInvalidSignature
		}
		fields[name] = content
	}
	if len(fields) != 3 {
		return signatureParts{}, ErrInvalidSignature
	}
	accessKey, date, region, err := parseCredentialScope(fields["Credential"])
	if err != nil {
		return signatureParts{}, err
	}
	if fields["SignedHeaders"] == "" || fields["Signature"] == "" {
		return signatureParts{}, ErrInvalidSignature
	}
	return signatureParts{
		AccessKey: accessKey, Date: date, Region: region,
		SignedHeaders: fields["SignedHeaders"], Signature: fields["Signature"],
	}, nil
}

func parseCredentialScope(value string) (string, string, string, error) {
	parts := strings.Split(value, "/")
	if len(parts) != 5 || parts[0] == "" || len(parts[1]) != 8 || parts[2] == "" || parts[3] != signatureService || parts[4] != signatureTerminal {
		return "", "", "", ErrInvalidSignature
	}
	return parts[0], parts[1], parts[2], nil
}

func canonicalRequest(request *http.Request, signedHeaders, payloadHash string, presigned bool) (string, error) {
	headers, err := canonicalHeaders(request, signedHeaders)
	if err != nil {
		return "", err
	}
	query, err := canonicalQuery(request.URL.Query(), presigned)
	if err != nil {
		return "", err
	}
	uri, err := canonicalURI(request.URL)
	if err != nil {
		return "", err
	}
	return request.Method + "\n" + uri + "\n" + query + "\n" + headers + "\n" + signedHeaders + "\n" + payloadHash, nil
}

func canonicalHeaders(request *http.Request, signedHeaders string) (string, error) {
	names := strings.Split(signedHeaders, ";")
	if len(names) == 0 || len(names) > 64 {
		return "", ErrInvalidSignature
	}
	var builder strings.Builder
	previous := ""
	hostSeen := false
	for _, name := range names {
		if name == "" || name != strings.ToLower(name) || name <= previous {
			return "", ErrInvalidSignature
		}
		previous = name
		var values []string
		if name == "host" {
			hostSeen = true
			values = []string{request.Host}
		} else {
			values = append([]string(nil), request.Header.Values(http.CanonicalHeaderKey(name))...)
		}
		if len(values) == 0 {
			return "", ErrInvalidSignature
		}
		for index := range values {
			values[index] = strings.Join(strings.Fields(values[index]), " ")
		}
		builder.WriteString(name)
		builder.WriteByte(':')
		builder.WriteString(strings.Join(values, ","))
		builder.WriteByte('\n')
	}
	if !hostSeen {
		return "", ErrInvalidSignature
	}
	return builder.String(), nil
}

type queryPair struct {
	name  string
	value string
}

func canonicalQuery(values url.Values, presigned bool) (string, error) {
	pairs := make([]queryPair, 0)
	for name, entries := range values {
		if presigned && strings.EqualFold(name, "X-Amz-Signature") {
			continue
		}
		if len(entries) == 0 {
			entries = []string{""}
		}
		for _, value := range entries {
			pairs = append(pairs, queryPair{name: awsEncode(name, true), value: awsEncode(value, true)})
		}
	}
	sort.Slice(pairs, func(left, right int) bool {
		if pairs[left].name == pairs[right].name {
			return pairs[left].value < pairs[right].value
		}
		return pairs[left].name < pairs[right].name
	})
	encoded := make([]string, len(pairs))
	for index, pair := range pairs {
		encoded[index] = pair.name + "=" + pair.value
	}
	return strings.Join(encoded, "&"), nil
}

func canonicalURI(value *url.URL) (string, error) {
	path := value.EscapedPath()
	if path == "" {
		path = "/"
	}
	decoded, err := url.PathUnescape(path)
	if err != nil {
		return "", ErrInvalidSignature
	}
	return awsEncode(decoded, false), nil
}

func awsEncode(value string, encodeSlash bool) string {
	const hexadecimal = "0123456789ABCDEF"
	var builder strings.Builder
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' || character == '~' ||
			(character == '/' && !encodeSlash) {
			builder.WriteByte(character)
			continue
		}
		builder.WriteByte('%')
		builder.WriteByte(hexadecimal[character>>4])
		builder.WriteByte(hexadecimal[character&15])
	}
	return builder.String()
}

func validPayloadHash(value string) bool {
	if value == "UNSIGNED-PAYLOAD" {
		return true
	}
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func signatureMatches(parts signatureParts, secret, canonical string) bool {
	canonicalHash := sha256.Sum256([]byte(canonical))
	scope := parts.Date + "/" + parts.Region + "/" + signatureService + "/" + signatureTerminal
	stringToSign := signatureAlgorithm + "\n" + parts.Timestamp.UTC().Format("20060102T150405Z") + "\n" + scope + "\n" + hex.EncodeToString(canonicalHash[:])
	expected := hmacSHA256(signingKey(secret, parts.Date, parts.Region), stringToSign)
	actual, err := hex.DecodeString(parts.Signature)
	if err != nil || len(actual) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare(actual, expected) == 1
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

func signatureForTest(request *http.Request, secret, accessKey, region, signedHeaders, payloadHash string, timestamp time.Time, presigned bool) (string, error) {
	canonical, err := canonicalRequest(request, signedHeaders, payloadHash, presigned)
	if err != nil {
		return "", err
	}
	parts := signatureParts{
		AccessKey: accessKey, Date: timestamp.UTC().Format("20060102"), Region: region,
		SignedHeaders: signedHeaders, Timestamp: timestamp,
	}
	return hex.EncodeToString(hmacSHA256(signingKey(secret, parts.Date, region),
		signatureAlgorithm+"\n"+timestamp.UTC().Format("20060102T150405Z")+"\n"+
			parts.Date+"/"+region+"/"+signatureService+"/"+signatureTerminal+"\n"+
			fmt.Sprintf("%x", sha256.Sum256([]byte(canonical))))), nil
}
