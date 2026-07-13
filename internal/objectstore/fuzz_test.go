package objectstore

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func FuzzS3Inputs(f *testing.F) {
	f.Add("objects.example.com:443", "/bucket/folder/object.txt", "bytes=0-15")
	f.Add("bad host", "/%zz", "bytes=1-0")

	f.Fuzz(func(t *testing.T, host, path, rangeValue string) {
		if len(host)+len(path)+len(rangeValue) > 128<<10 {
			t.Skip()
		}

		_, _ = canonicalRequestHost(host)
		_, _, _ = parseS3Path(path)
		size := int64(len(path)%4096 + 1)
		offset, length, _, err := parseRange(rangeValue, size)
		if err == nil && (offset < 0 || length < 0 || offset > size || length > size-offset) {
			t.Fatalf("range escaped bounds: offset=%d length=%d size=%d", offset, length, size)
		}
	})
}

func FuzzSigV4Inputs(f *testing.F) {
	timestamp := time.Date(2026, 7, 13, 10, 11, 12, 0, time.UTC)
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
		f.Fatal(err)
	}
	f.Add(
		"/bucket/object",
		"version=1&version=2",
		"AWS4-HMAC-SHA256 Credential=bad,SignedHeaders=host,Signature=00",
		"20260713T101112Z",
		"UNSIGNED-PAYLOAD",
		"host",
	)
	f.Add("/%zz", "%zz", "", "", "", "")

	f.Fuzz(func(t *testing.T, path, rawQuery, authorization, amzDate, payloadHash, signedHeaders string) {
		if len(path)+len(rawQuery)+len(authorization)+len(amzDate)+len(payloadHash)+len(signedHeaders) > 128<<10 {
			t.Skip()
		}
		request := &http.Request{
			Method: http.MethodGet,
			URL: &url.URL{
				Scheme:   "https",
				Host:     "objects.example.com",
				Path:     path,
				RawQuery: rawQuery,
			},
			Host:   "objects.example.com",
			Header: make(http.Header),
		}
		request.Header.Set("Authorization", authorization)
		request.Header.Set("X-Amz-Date", amzDate)
		request.Header.Set("X-Amz-Content-Sha256", payloadHash)
		request.Header.Set("X-Fuzz-Signed-Headers", signedHeaders)
		_, _ = verifier.Verify(context.Background(), request)
	})
}
