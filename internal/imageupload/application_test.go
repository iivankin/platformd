package imageupload

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/servicesource"
	"github.com/iivankin/platformd/internal/state"
)

type uploadTestStore struct {
	service state.ServiceDesired
	upload  state.ImageUpload
}

func (store *uploadTestStore) ProjectByName(_ context.Context, name string) (state.ProjectSummary, error) {
	if name != store.service.ProjectName && name != store.service.ProjectID {
		return state.ProjectSummary{}, state.ErrProjectNotFound
	}
	return state.ProjectSummary{ID: store.service.ProjectID, Name: store.service.ProjectName}, nil
}

func (store *uploadTestStore) ProjectResourceByName(_ context.Context, projectID, name string) (state.ProjectResource, error) {
	if projectID != store.service.ProjectID || (name != store.service.Name && name != store.service.ID) {
		return state.ProjectResource{}, state.ErrProjectResourceNotFound
	}
	return state.ProjectResource{ID: store.service.ID, Kind: "service", Name: store.service.Name}, nil
}

func (store *uploadTestStore) Service(context.Context, string, string) (state.ServiceDesired, error) {
	return store.service, nil
}

func (store *uploadTestStore) BeginImageUpload(_ context.Context, input state.BeginImageUploadInput) ([]string, error) {
	store.upload = state.ImageUpload{
		ID: input.ID, ServiceID: input.ServiceID, Tag: input.Tag,
		ExpectedLength: input.ExpectedLength, ExpectedSHA256: input.ExpectedSHA256,
		TemporaryPath: input.TemporaryPath, Identity: input.Identity, Status: "uploading",
		CreatedAtMillis: input.CreatedAtMillis, UpdatedAtMillis: input.CreatedAtMillis,
		ExpiresAtMillis: input.ExpiresAtMillis,
	}
	return nil, nil
}

func (store *uploadTestStore) ImageUpload(_ context.Context, uploadID, serviceID string) (state.ImageUpload, error) {
	if store.upload.ID != uploadID || store.upload.ServiceID != serviceID {
		return state.ImageUpload{}, state.ErrImageUploadNotFound
	}
	return store.upload, nil
}

func (store *uploadTestStore) AdvanceImageUpload(_ context.Context, uploadID string, expectedOffset, nextOffset, updatedAtMillis int64) error {
	if store.upload.ID != uploadID || store.upload.ReceivedLength != expectedOffset {
		return state.ErrImageUploadChanged
	}
	store.upload.ReceivedLength = nextOffset
	store.upload.UpdatedAtMillis = updatedAtMillis
	return nil
}

func (*uploadTestStore) CreateImageRevision(context.Context, state.CreateImageRevisionInput) error {
	return errors.New("unexpected image processing")
}
func (*uploadTestStore) SetImageRevisionImported(context.Context, string, string, string, string, int64) error {
	return errors.New("unexpected image processing")
}
func (*uploadTestStore) CompleteImageUpload(context.Context, string, string, string, int64, int64, int64) error {
	return errors.New("unexpected image processing")
}
func (store *uploadTestStore) FailImageUpload(_ context.Context, uploadID, code, message string, failedAtMillis, expiresAtMillis int64) error {
	if store.upload.ID != uploadID {
		return state.ErrImageUploadNotFound
	}
	store.upload.Status = "failed"
	store.upload.ErrorCode = code
	store.upload.ErrorMessage = message
	store.upload.UpdatedAtMillis = failedAtMillis
	store.upload.ExpiresAtMillis = expiresAtMillis
	return nil
}
func (*uploadTestStore) ActiveImageUploads(context.Context, int64, bool) ([]state.ImageUpload, error) {
	return nil, nil
}
func (*uploadTestStore) CancelImageUploads(context.Context, int64, bool, int64) ([]string, error) {
	return nil, nil
}

type uploadTestEngine struct{}

func (uploadTestEngine) Pull(context.Context, containerengine.PullRequest) (containerengine.Image, error) {
	return containerengine.Image{}, errors.New("unexpected image processing")
}

type uploadTestRuntime struct{}

func (uploadTestRuntime) DeployUploadedProduction(context.Context, string, string, string, string, containerengine.Image, state.ImageUploadIdentity) error {
	return errors.New("unexpected image processing")
}
func (uploadTestRuntime) DeployUploadedPreview(context.Context, string, string, string, string, string, containerengine.Image, state.ImageUploadIdentity) (string, error) {
	return "", errors.New("unexpected image processing")
}

type uploadTestGrowth struct{}

func (uploadTestGrowth) PermitGrowth(context.Context) error { return nil }

type oidcRoundTrip func(*http.Request) (*http.Response, error)

func (roundTrip oidcRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestChunkUploadAndStatusShareTheOIDCEndpoint(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks, err := json.Marshal(map[string]any{"keys": []map[string]string{{
		"alg": "RS256", "e": base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1}),
		"kid": "test-key", "kty": "RSA", "n": base64.RawURLEncoding.EncodeToString(privateKey.N.Bytes()),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: oidcRoundTrip(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header), Request: request,
			Body: io.NopCloser(bytes.NewReader(jwks)),
		}, nil
	})}
	const endpoint = "/public/api/v1/projects/shop/services/api/image"
	const audience = "https://platform.example.com" + endpoint
	token := signedUploadToken(t, privateKey, now, audience)
	store := &uploadTestStore{service: state.ServiceDesired{
		ID: "service-id", ProjectID: "project-id", ProjectName: "shop", Name: "api", Enabled: true,
		Snapshot: serviceconfig.Snapshot{Source: servicesource.Source{
			Type: servicesource.DockerImageUpload,
			DockerUpload: &servicesource.DockerUpload{
				Repository: "acme/backend", Branch: "main", Workflows: []string{"deploy.yml"},
			},
		}},
	}}
	application, err := New(Config{
		Context: context.Background(), Store: store, Engine: uploadTestEngine{}, Runtime: uploadTestRuntime{},
		Growth: uploadTestGrowth{}, Verifier: NewOIDCVerifier(client, func() time.Time { return now }),
		PublicHostname: "platform.example.com", UploadRoot: t.TempDir(), ImageRoot: t.TempDir(),
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, endpoint, bytes.NewBufferString("ab"))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Upload-ID", "upload-identifier-0001")
	request.Header.Set("Upload-Tag", "latest")
	request.Header.Set("Upload-Offset", "0")
	request.Header.Set("Upload-Length", "10737418240")
	request.Header.Set("Upload-SHA256", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	response := httptest.NewRecorder()
	application.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || response.Header().Get("Upload-Offset") != "2" {
		t.Fatalf("first chunk response = %d %s", response.Code, response.Body.String())
	}
	content, err := os.ReadFile(store.upload.TemporaryPath)
	if err != nil || string(content) != "ab" || store.upload.ExpectedLength != 10<<30 {
		t.Fatalf("stored chunk/length = %q/%d, error = %v", content, store.upload.ExpectedLength, err)
	}

	statusRequest := httptest.NewRequest(http.MethodGet, endpoint, nil)
	statusRequest.Header.Set("Authorization", "Bearer "+token)
	statusRequest.Header.Set("Upload-ID", store.upload.ID)
	statusResponse := httptest.NewRecorder()
	application.Handler().ServeHTTP(statusResponse, statusRequest)
	if statusResponse.Code != http.StatusOK || statusResponse.Header().Get("Upload-Offset") != "2" {
		t.Fatalf("status response = %d %s", statusResponse.Code, statusResponse.Body.String())
	}

	mismatch := httptest.NewRequest(http.MethodPost, endpoint, bytes.NewBufferString("cd"))
	mismatch.Header = request.Header.Clone()
	mismatch.Header.Set("Upload-Offset", "0")
	mismatchResponse := httptest.NewRecorder()
	application.Handler().ServeHTTP(mismatchResponse, mismatch)
	if mismatchResponse.Code != http.StatusConflict || mismatchResponse.Header().Get("Upload-Offset") != "" {
		t.Fatalf("offset mismatch response = %d %v %s", mismatchResponse.Code, mismatchResponse.Header(), mismatchResponse.Body.String())
	}
	if store.upload.Status != "failed" || store.upload.ErrorCode != "offset_mismatch" {
		t.Fatalf("upload after mismatch = %+v", store.upload)
	}

	missing := httptest.NewRequest(http.MethodPost, endpoint, bytes.NewBufferString("x"))
	missing.Header = request.Header.Clone()
	missing.Header.Del("Authorization")
	missingResponse := httptest.NewRecorder()
	application.Handler().ServeHTTP(missingResponse, missing)
	if missingResponse.Code != http.StatusUnauthorized {
		t.Fatalf("missing auth response = %d %s", missingResponse.Code, missingResponse.Body.String())
	}
}

func signedUploadToken(t *testing.T, key *rsa.PrivateKey, now time.Time, audience string) string {
	t.Helper()
	header, err := json.Marshal(oidcHeader{Algorithm: "RS256", KeyID: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := json.Marshal(oidcClaims{
		Audience: oidcAudience{audience}, Issuer: githubIssuer,
		ExpiresAt: now.Add(5 * time.Minute).Unix(), IssuedAt: now.Unix(),
		Repository: "acme/backend", Ref: "refs/heads/main", SHA: "commit",
		Workflow: "Deploy", WorkflowRef: "acme/backend/.github/workflows/deploy.yml@refs/heads/main",
		Actor: "developer", RunID: "123", RunAttempt: "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	encode := base64.RawURLEncoding.EncodeToString
	signingInput := encode(header) + "." + encode(claims)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signingInput + "." + encode(signature)
}
