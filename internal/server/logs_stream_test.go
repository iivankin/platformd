package server_test

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/containerlogs"
	"github.com/iivankin/platformd/internal/server"
)

type liveLogRepository struct {
	mu       sync.Mutex
	records  []containerlogs.Record
	queries  int
	revision int
}

func (*liveLogRepository) DownloadServiceLogs(context.Context, string, containerlogs.DownloadQuery, io.Writer) (containerlogs.DownloadResult, error) {
	return containerlogs.DownloadResult{}, nil
}

func (repository *liveLogRepository) ServiceLogRevision(context.Context, string, string, string, string) (string, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return fmt.Sprint(repository.revision), nil
}

func (repository *liveLogRepository) ServiceLogs(_ context.Context, _, _, _, _ string, limit int) (containerlogs.Window, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.queries++
	records := append([]containerlogs.Record(nil), repository.records...)
	if limit > 0 && len(records) > limit {
		records = records[len(records)-limit:]
	}
	return containerlogs.Window{Records: records}, nil
}

func TestServiceLogWebSocketSendsSnapshotAndNewRecords(t *testing.T) {
	t.Parallel()

	repository := &liveLogRepository{records: []containerlogs.Record{{
		Timestamp: time.Unix(1, 0).UTC(), Stream: "stdout", Text: "ready",
		DeploymentID: "deployment", AttemptID: "attempt",
	}}, revision: 1}
	direct := server.Handler(server.DefaultMeta("ready"), server.WithLogs("admin.example.com", repository))
	tlsServer := httptest.NewTLSServer(access.ProtectAdmin("admin.example.com", projectVerifier{}, direct))
	defer tlsServer.Close()

	connection, response, err := websocket.Dial(context.Background(), "wss://admin.example.com/api/v1/projects/project/services/service/logs/stream?limit=20", logDialOptions(tlsServer, "https://admin.example.com"))
	if err != nil {
		if response != nil {
			t.Fatalf("dial status %d: %v", response.StatusCode, err)
		}
		t.Fatal(err)
	}
	defer connection.CloseNow()
	var snapshot struct {
		Type    string                 `json:"type"`
		Records []containerlogs.Record `json:"records"`
	}
	if err := wsjson.Read(context.Background(), connection, &snapshot); err != nil || snapshot.Type != "snapshot" || len(snapshot.Records) != 1 || snapshot.Records[0].Text != "ready" {
		t.Fatalf("snapshot = %+v, %v", snapshot, err)
	}
	repository.mu.Lock()
	repository.records = append(repository.records, containerlogs.Record{
		Timestamp: time.Unix(2, 0).UTC(), Stream: "stderr", Text: "warning",
		DeploymentID: "deployment", AttemptID: "attempt",
	})
	repository.revision++
	repository.mu.Unlock()
	readCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var update struct {
		Type    string                 `json:"type"`
		Records []containerlogs.Record `json:"records"`
	}
	if err := wsjson.Read(readCtx, connection, &update); err != nil || update.Type != "records" || len(update.Records) != 1 || update.Records[0].Text != "warning" {
		t.Fatalf("update = %+v, %v", update, err)
	}
}

func TestServiceLogWebSocketRejectsWrongOriginBeforeRead(t *testing.T) {
	t.Parallel()

	repository := &liveLogRepository{}
	direct := server.Handler(server.DefaultMeta("ready"), server.WithLogs("admin.example.com", repository))
	tlsServer := httptest.NewTLSServer(access.ProtectAdmin("admin.example.com", projectVerifier{}, direct))
	defer tlsServer.Close()
	_, response, err := websocket.Dial(context.Background(), "wss://admin.example.com/api/v1/projects/project/services/service/logs/stream", logDialOptions(tlsServer, "https://evil.example.com"))
	if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong origin response = %#v, %v", response, err)
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.queries != 0 {
		t.Fatalf("repository queried %d times", repository.queries)
	}
}

func logDialOptions(server *httptest.Server, origin string) *websocket.DialOptions {
	address := server.Listener.Addr().String()
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // Ephemeral test certificate.
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", address)
		},
	}
	return &websocket.DialOptions{
		HTTPClient: &http.Client{Transport: transport},
		HTTPHeader: http.Header{
			"Origin":                  []string{origin},
			"Cf-Access-Jwt-Assertion": []string{"assertion"},
		},
	}
}
