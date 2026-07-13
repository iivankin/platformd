package objectstore

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const (
	testAccessKey = "ps3_018bcfe5687b7fffbfffffffffffffff"
	testSecret    = "test-secret-for-signature"
)

func testVerifier(t *testing.T, timestamp time.Time) *SigV4Verifier {
	t.Helper()
	verifier, err := NewSigV4Verifier(SigV4Config{
		Now: func() time.Time { return timestamp },
		Resolve: func(_ context.Context, accessKey string) (Credential, error) {
			if accessKey != testAccessKey {
				return Credential{}, ErrInvalidSignature
			}
			return Credential{Secret: testSecret, Permission: "read_write"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return verifier
}

func TestSigV4HeaderAuthenticationAndTamperDetection(t *testing.T) {
	t.Parallel()
	timestamp := time.Date(2026, 7, 13, 10, 11, 12, 0, time.UTC)
	request := httptest.NewRequest(http.MethodGet, "https://objects.example.com/bucket/folder/a%20b.txt?version=2&version=1", nil)
	payload := sha256.Sum256(nil)
	payloadHash := fmt.Sprintf("%x", payload)
	request.Header.Set("X-Amz-Date", timestamp.Format("20060102T150405Z"))
	request.Header.Set("X-Amz-Content-Sha256", payloadHash)
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	signature, err := signatureForTest(request, testSecret, testAccessKey, Region, signedHeaders, payloadHash, timestamp, false)
	if err != nil {
		t.Fatal(err)
	}
	scope := timestamp.Format("20060102") + "/" + Region + "/s3/aws4_request"
	request.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+testAccessKey+"/"+scope+",SignedHeaders="+signedHeaders+",Signature="+signature)
	if _, err := testVerifier(t, timestamp).Verify(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.URL.RawQuery += "&tampered=1"
	if _, err := testVerifier(t, timestamp).Verify(context.Background(), request); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("tampered query error = %v", err)
	}
}

func TestSigV4RejectsAnyRegionOtherThanUSEast1(t *testing.T) {
	t.Parallel()
	timestamp := time.Date(2026, 7, 13, 10, 11, 12, 0, time.UTC)
	request := httptest.NewRequest(http.MethodGet, "https://objects.example.com/bucket/object", nil)
	payload := sha256.Sum256(nil)
	payloadHash := fmt.Sprintf("%x", payload)
	request.Header.Set("X-Amz-Date", timestamp.Format("20060102T150405Z"))
	request.Header.Set("X-Amz-Content-Sha256", payloadHash)
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	const wrongRegion = "eu-central-1"
	signature, err := signatureForTest(request, testSecret, testAccessKey, wrongRegion, signedHeaders, payloadHash, timestamp, false)
	if err != nil {
		t.Fatal(err)
	}
	scope := timestamp.Format("20060102") + "/" + wrongRegion + "/s3/aws4_request"
	request.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+testAccessKey+"/"+scope+",SignedHeaders="+signedHeaders+",Signature="+signature)
	if _, err := testVerifier(t, timestamp).Verify(context.Background(), request); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("wrong region error = %v", err)
	}
}

func TestSigV4PresignedRequestExpiresAndSupportsDuplicateQuery(t *testing.T) {
	t.Parallel()
	timestamp := time.Date(2026, 7, 13, 10, 11, 12, 0, time.UTC)
	request := httptest.NewRequest(http.MethodGet, "https://objects.example.com/bucket/object", nil)
	query := url.Values{}
	query.Set("X-Amz-Algorithm", signatureAlgorithm)
	query.Set("X-Amz-Credential", testAccessKey+"/"+timestamp.Format("20060102")+"/"+Region+"/s3/aws4_request")
	query.Set("X-Amz-Date", timestamp.Format("20060102T150405Z"))
	query.Set("X-Amz-Expires", "60")
	query.Set("X-Amz-SignedHeaders", "host")
	query.Add("response-content-type", "text/plain")
	query.Add("response-content-type", "application/json")
	request.URL.RawQuery = query.Encode()
	signature, err := signatureForTest(request, testSecret, testAccessKey, Region, "host", "UNSIGNED-PAYLOAD", timestamp, true)
	if err != nil {
		t.Fatal(err)
	}
	query.Set("X-Amz-Signature", signature)
	request.URL.RawQuery = query.Encode()
	if _, err := testVerifier(t, timestamp.Add(59*time.Second)).Verify(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := testVerifier(t, timestamp.Add(61*time.Second)).Verify(context.Background(), request); !errors.Is(err, ErrExpiredSignature) {
		t.Fatalf("expired presign error = %v", err)
	}
}

func TestSigV4RejectsDuplicateAuthenticationFields(t *testing.T) {
	t.Parallel()
	timestamp := time.Date(2026, 7, 13, 10, 11, 12, 0, time.UTC)
	request := httptest.NewRequest(http.MethodGet, "https://objects.example.com/bucket/object", nil)
	query := request.URL.Query()
	query.Add("X-Amz-Algorithm", signatureAlgorithm)
	query.Add("X-Amz-Algorithm", signatureAlgorithm)
	query.Set("X-Amz-Credential", testAccessKey+"/"+timestamp.Format("20060102")+"/"+Region+"/s3/aws4_request")
	query.Set("X-Amz-Date", timestamp.Format("20060102T150405Z"))
	query.Set("X-Amz-Expires", "60")
	query.Set("X-Amz-Signature", strings.Repeat("0", 64))
	query.Set("X-Amz-SignedHeaders", "host")
	request.URL.RawQuery = query.Encode()
	if _, err := testVerifier(t, timestamp).Verify(context.Background(), request); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("duplicate presign field error = %v", err)
	}
}
