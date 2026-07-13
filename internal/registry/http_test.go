package registry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/state"
)

type registryHTTPFixture struct {
	application *Application
	handler     http.Handler
	admission   *admission.Gate
	private     CreateRepositoryResult
	public      CreateRepositoryResult
}

func TestDistributionRejectsAuthenticatedPushWhilePlatformUpdates(t *testing.T) {
	t.Parallel()
	fixture := newRegistryHTTPFixture(t)
	update, _, err := fixture.admission.TryUpdate()
	if err != nil {
		t.Fatal(err)
	}
	defer update.Release()
	response := fixture.request(t, http.MethodPost, "/v2/"+fixture.private.Repository.Name+"/blobs/uploads/", nil, &fixture.private)
	assertRegistryResponse(t, response, http.StatusConflict)
	assertDistributionError(t, response, "UNAVAILABLE")
}

func TestDistributionHTTPContract(t *testing.T) {
	t.Parallel()
	fixture := newRegistryHTTPFixture(t)

	response := fixture.request(t, http.MethodGet, "/v2/", nil, nil)
	assertRegistryResponse(t, response, http.StatusUnauthorized)
	assertDistributionError(t, response, "UNAUTHORIZED")
	response = fixture.request(t, http.MethodGet, "/v2/", nil, &fixture.private)
	assertRegistryResponse(t, response, http.StatusOK)
	if body := readResponse(t, response); body != "{}" {
		t.Fatalf("authenticated v2 body = %q", body)
	}

	response = fixture.request(t, http.MethodPost, "/v2/team/private/blobs/uploads/", nil, nil)
	assertRegistryResponse(t, response, http.StatusUnauthorized)
	if challenge := response.Header.Get("WWW-Authenticate"); challenge != `Bearer realm="http://example.com/v2/token",service="example.com",scope="repository:team/private:pull,push"` {
		t.Fatalf("challenge = %q", challenge)
	}
	assertDistributionError(t, response, "UNAUTHORIZED")

	privateDigest := fixture.uploadBlob(t, fixture.private, "private layer")
	response = fixture.request(t, http.MethodGet, "/v2/team/private/blobs/"+privateDigest, nil, nil)
	assertRegistryResponse(t, response, http.StatusUnauthorized)
	_ = readResponse(t, response)

	response = fixture.request(t, http.MethodGet, "/v2/team/public/blobs/"+privateDigest, nil, nil)
	assertRegistryResponse(t, response, http.StatusNotFound)
	assertDistributionError(t, response, "BLOB_UNKNOWN")

	privateManifest := fixture.putManifest(t, fixture.private, "latest", privateDigest)
	response = fixture.request(t, http.MethodHead, "/v2/team/private/manifests/latest", nil, &fixture.private)
	assertRegistryResponse(t, response, http.StatusOK)
	if response.Header.Get("Docker-Content-Digest") != privateManifest {
		t.Fatalf("manifest digest = %q", response.Header.Get("Docker-Content-Digest"))
	}
	if response.Header.Get("Content-Length") == "" || readResponse(t, response) != "" {
		t.Fatal("manifest HEAD did not preserve length with an empty body")
	}

	response = fixture.request(t, http.MethodGet, "/v2/team/private/manifests/"+privateManifest, nil, &fixture.private)
	assertRegistryResponse(t, response, http.StatusOK)
	if response.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("private cache policy = %q", response.Header.Get("Cache-Control"))
	}
	_ = readResponse(t, response)

	fixture.putManifest(t, fixture.private, "alpha", privateDigest)
	fixture.putManifest(t, fixture.private, "zulu", privateDigest)
	response = fixture.request(t, http.MethodGet, "/v2/team/private/tags/list?n=1", nil, &fixture.private)
	assertRegistryResponse(t, response, http.StatusOK)
	if link := response.Header.Get("Link"); link != `</v2/team/private/tags/list?n=1&last=alpha>; rel="next"` {
		t.Fatalf("pagination link = %q", link)
	}
	var tagPage struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(response.Body).Decode(&tagPage); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if tagPage.Name != "team/private" || len(tagPage.Tags) != 1 || tagPage.Tags[0] != "alpha" {
		t.Fatalf("tag page = %+v", tagPage)
	}

	publicDigest := fixture.uploadBlob(t, fixture.public, "public layer")
	publicManifest := fixture.putManifest(t, fixture.public, "latest", publicDigest)
	response = fixture.request(t, http.MethodGet, "/v2/team/public/blobs/"+publicDigest, nil, nil)
	assertRegistryResponse(t, response, http.StatusOK)
	if response.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" || readResponse(t, response) != "public layer" {
		t.Fatal("anonymous public blob response is invalid")
	}

	response = fixture.request(t, http.MethodGet, "/v2/team/public/manifests/latest", nil, nil)
	assertRegistryResponse(t, response, http.StatusOK)
	if response.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("mutable public manifest cache policy = %q", response.Header.Get("Cache-Control"))
	}
	_ = readResponse(t, response)
	response = fixture.request(t, http.MethodGet, "/v2/team/public/manifests/"+publicManifest, nil, nil)
	assertRegistryResponse(t, response, http.StatusOK)
	if response.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("immutable public manifest cache policy = %q", response.Header.Get("Cache-Control"))
	}
	_ = readResponse(t, response)
}

func TestBlobUploadStatusRangeAndMountFallback(t *testing.T) {
	t.Parallel()
	fixture := newRegistryHTTPFixture(t)
	uploadPath := "/v2/team/private/blobs/uploads/?mount=sha256:" + strings.Repeat("a", 64) + "&from=another/repository"
	response := fixture.request(t, http.MethodPost, uploadPath, nil, &fixture.private)
	assertRegistryResponse(t, response, http.StatusAccepted)
	location := response.Header.Get("Location")
	if !strings.HasPrefix(location, "/v2/team/private/blobs/uploads/") || response.Header.Get("Range") != "0-0" {
		t.Fatalf("upload start headers = location %q range %q", location, response.Header.Get("Range"))
	}
	_ = readResponse(t, response)

	headers := http.Header{"Content-Range": []string{"0-5"}}
	response = fixture.requestWithHeaders(t, http.MethodPatch, location, strings.NewReader("hello "), &fixture.private, headers)
	assertRegistryResponse(t, response, http.StatusAccepted)
	if response.Header.Get("Range") != "0-5" {
		t.Fatalf("patch range = %q", response.Header.Get("Range"))
	}
	_ = readResponse(t, response)

	response = fixture.request(t, http.MethodGet, location, nil, &fixture.private)
	assertRegistryResponse(t, response, http.StatusNoContent)
	if response.Header.Get("Range") != "0-5" {
		t.Fatalf("status range = %q", response.Header.Get("Range"))
	}
	_ = readResponse(t, response)

	payload := "hello world"
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(payload)))
	response = fixture.request(t, http.MethodPut, location+"?digest="+url.QueryEscape(digest), strings.NewReader("world"), &fixture.private)
	assertRegistryResponse(t, response, http.StatusCreated)
	if response.Header.Get("Docker-Content-Digest") != digest || response.Header.Get("Location") != "/v2/team/private/blobs/"+digest {
		t.Fatalf("finalize headers = digest %q location %q", response.Header.Get("Docker-Content-Digest"), response.Header.Get("Location"))
	}
	_ = readResponse(t, response)

	response = fixture.requestWithHeaders(t, http.MethodGet, "/v2/team/private/blobs/"+digest, nil, &fixture.private, http.Header{"Range": []string{"bytes=6-"}})
	assertRegistryResponse(t, response, http.StatusPartialContent)
	if response.Header.Get("Content-Range") != "bytes 6-10/11" || response.Header.Get("Content-Length") != "5" || readResponse(t, response) != "world" {
		t.Fatal("blob byte range response is invalid")
	}

	response = fixture.request(t, http.MethodHead, "/v2/team/private/blobs/"+digest, nil, &fixture.private)
	assertRegistryResponse(t, response, http.StatusOK)
	if response.Header.Get("Content-Length") != "11" || readResponse(t, response) != "" {
		t.Fatal("blob HEAD response is invalid")
	}
}

func TestTokenExchangeAnonymousScopePermissionAndImmediateRevoke(t *testing.T) {
	t.Parallel()
	fixture := newRegistryHTTPFixture(t)
	requestToken := func(repository, actions string, credential *CreateRepositoryResult) *http.Response {
		query := url.Values{
			"service": []string{"example.com"},
			"scope":   []string{"repository:" + repository + ":" + actions},
		}
		request := httptest.NewRequest(http.MethodGet, "/v2/token?"+query.Encode(), nil)
		if credential != nil {
			request.SetBasicAuth(credential.Username, credential.Secret)
		}
		response := httptest.NewRecorder()
		fixture.handler.ServeHTTP(response, request)
		return response.Result()
	}

	response := requestToken(fixture.public.Repository.Name, "pull", nil)
	assertRegistryResponse(t, response, http.StatusOK)
	var anonymous registryTokenResponse
	if err := json.NewDecoder(response.Body).Decode(&anonymous); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	claims, err := fixture.application.tokens.verify(anonymous.Token, "example.com", fixture.application.now())
	if err != nil || claims.CredentialID != "" || !tokenAllows(claims.Actions, false) {
		t.Fatalf("anonymous token claims = %+v, %v", claims, err)
	}
	response = requestToken(fixture.private.Repository.Name, "pull", nil)
	assertRegistryResponse(t, response, http.StatusUnauthorized)
	assertDistributionError(t, response, "UNAUTHORIZED")

	pull, err := fixture.application.CreateCredential(context.Background(), CreateCredentialInput{
		RepositoryID: fixture.private.Repository.ID, Name: "token-reader", Permission: "pull",
		Actor: Actor{Kind: "access", ID: "user", Email: "admin@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	pullRepository := CreateRepositoryResult{
		Repository: fixture.private.Repository, Username: pull.Username, Secret: pull.Secret,
	}
	response = requestToken(fixture.private.Repository.Name, "pull,push", &pullRepository)
	assertRegistryResponse(t, response, http.StatusForbidden)
	assertDistributionError(t, response, "DENIED")
	response = requestToken(fixture.private.Repository.Name, "pull", &pullRepository)
	assertRegistryResponse(t, response, http.StatusOK)
	var authorized registryTokenResponse
	if err := json.NewDecoder(response.Body).Decode(&authorized); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	used, err := fixture.application.store.RegistryCredential(context.Background(), pull.Credential.ID)
	if err != nil || used.LastUsedAtMillis != fixture.application.now().UnixMilli() {
		t.Fatalf("token credential last used = %+v, %v", used, err)
	}
	if _, err := fixture.application.DeleteCredential(
		context.Background(), fixture.private.Repository.ID, pull.Credential.ID,
		Actor{Kind: "access", ID: "user", Email: "admin@example.com"},
	); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v2/team/private/tags/list", nil)
	request.Header.Set("Authorization", "Bearer "+authorized.Token)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	response = recorder.Result()
	assertRegistryResponse(t, response, http.StatusUnauthorized)
	assertDistributionError(t, response, "UNAUTHORIZED")
}

func newRegistryHTTPFixture(t *testing.T) registryHTTPFixture {
	t.Helper()
	ctx := context.Background()
	store, err := state.Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	payloads, err := NewPayloadStore(filepath.Join(t.TempDir(), "registry"))
	if err != nil {
		t.Fatal(err)
	}
	application, err := NewApplication(store, payloads, cryptobox.MasterKey{1, 2, 3}, nil, nil, func() time.Time {
		return time.UnixMilli(1_720_000_000_000)
	})
	if err != nil {
		t.Fatal(err)
	}
	gate := admission.New()
	handler, err := NewHTTPHandler(application, allowRegistryFailures{}, gate)
	if err != nil {
		t.Fatal(err)
	}
	create := func(name string, public bool) CreateRepositoryResult {
		result, err := application.CreateRepository(ctx, CreateRepositoryInput{
			Name: name, PublicPull: public,
			Actor: Actor{Kind: "access", ID: "user", Email: "admin@example.com"},
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	return registryHTTPFixture{
		application: application, handler: handler, admission: gate,
		private: create("team/private", false), public: create("team/public", true),
	}
}

type allowRegistryFailures struct{}

func (allowRegistryFailures) Permit(string, string) (bool, time.Duration) { return true, 0 }
func (allowRegistryFailures) Failed(string, string)                       {}
func (allowRegistryFailures) Succeeded(string, string)                    {}

func (fixture registryHTTPFixture) uploadBlob(t *testing.T, repository CreateRepositoryResult, payload string) string {
	t.Helper()
	response := fixture.request(t, http.MethodPost, "/v2/"+repository.Repository.Name+"/blobs/uploads/", nil, &repository)
	assertRegistryResponse(t, response, http.StatusAccepted)
	location := response.Header.Get("Location")
	_ = readResponse(t, response)
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(payload)))
	response = fixture.request(t, http.MethodPut, location+"?digest="+url.QueryEscape(digest), strings.NewReader(payload), &repository)
	assertRegistryResponse(t, response, http.StatusCreated)
	_ = readResponse(t, response)
	return digest
}

func (fixture registryHTTPFixture) putManifest(t *testing.T, repository CreateRepositoryResult, tag, blobDigest string) string {
	t.Helper()
	body := fmt.Sprintf(`{"schemaVersion":2,"mediaType":%q,"config":{"digest":%q},"layers":[{"digest":%q}]}`,
		OCIImageManifestMediaType, blobDigest, blobDigest)
	headers := http.Header{"Content-Type": []string{OCIImageManifestMediaType}}
	response := fixture.requestWithHeaders(t, http.MethodPut, "/v2/"+repository.Repository.Name+"/manifests/"+tag, strings.NewReader(body), &repository, headers)
	assertRegistryResponse(t, response, http.StatusCreated)
	digest := response.Header.Get("Docker-Content-Digest")
	if digest == "" || response.Header.Get("Location") != "/v2/"+repository.Repository.Name+"/manifests/"+digest {
		t.Fatalf("manifest put headers = digest %q location %q", digest, response.Header.Get("Location"))
	}
	_ = readResponse(t, response)
	return digest
}

func (fixture registryHTTPFixture) request(t *testing.T, method, target string, body io.Reader, credential *CreateRepositoryResult) *http.Response {
	t.Helper()
	return fixture.requestWithHeaders(t, method, target, body, credential, nil)
}

func (fixture registryHTTPFixture) requestWithHeaders(t *testing.T, method, target string, body io.Reader, credential *CreateRepositoryResult, headers http.Header) *http.Response {
	t.Helper()
	request := httptest.NewRequest(method, target, body)
	for name, values := range headers {
		request.Header[name] = append([]string(nil), values...)
	}
	if credential != nil {
		write := strings.Contains(target, "/blobs/uploads/") || method == http.MethodPost || method == http.MethodPatch || method == http.MethodPut || method == http.MethodDelete
		token := fixture.registryToken(t, request.Host, credential, write, target != "/v2/")
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	return recorder.Result()
}

func (fixture registryHTTPFixture) registryToken(t *testing.T, host string, credential *CreateRepositoryResult, write, scoped bool) string {
	t.Helper()
	query := url.Values{"service": []string{host}}
	if scoped {
		actions := "pull"
		if write {
			actions = "pull,push"
		}
		query.Set("scope", "repository:"+credential.Repository.Name+":"+actions)
	}
	request := httptest.NewRequest(http.MethodGet, "/v2/token?"+query.Encode(), nil)
	request.Host = host
	request.SetBasicAuth(credential.Username, credential.Secret)
	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("token status = %d: %s", response.Code, response.Body.String())
	}
	var token registryTokenResponse
	if err := json.NewDecoder(response.Body).Decode(&token); err != nil {
		t.Fatal(err)
	}
	if token.Token == "" || token.Token != token.AccessToken || token.ExpiresIn != int(RegistryTokenLifetime/time.Second) {
		t.Fatalf("token response = %+v", token)
	}
	return token.Token
}

func assertRegistryResponse(t *testing.T, response *http.Response, status int) {
	t.Helper()
	if response.StatusCode != status {
		body := readResponse(t, response)
		t.Fatalf("status = %d, want %d, body = %s", response.StatusCode, status, body)
	}
	if version := response.Header.Get("Docker-Distribution-API-Version"); version != distributionAPIVersion {
		t.Fatalf("distribution API version = %q", version)
	}
}

func assertDistributionError(t *testing.T, response *http.Response, code string) {
	t.Helper()
	var envelope distributionErrorEnvelope
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(envelope.Errors) != 1 || envelope.Errors[0].Code != code {
		t.Fatalf("distribution errors = %+v", envelope.Errors)
	}
}

func readResponse(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(bytes.TrimSpace(body))
}
