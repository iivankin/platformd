package server_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/selfupdate"
	"github.com/iivankin/platformd/internal/server"
)

type updaterStub struct {
	status selfupdate.Status
	result selfupdate.Result
	err    error
}

func (updater updaterStub) Check(context.Context) (selfupdate.Status, error) {
	return updater.status, updater.err
}

func (updater updaterStub) Apply(context.Context) (selfupdate.Result, error) {
	return updater.result, updater.err
}

func TestSelfUpdateReturnsVerifiedAvailability(t *testing.T) {
	t.Parallel()
	handler := server.Handler(server.DefaultMeta("ready"),
		server.WithAdmission(admission.New()),
		server.WithSelfUpdate(updaterStub{status: selfupdate.Status{
			CurrentVersion: "1.0.0", LatestVersion: "2.0.0",
			UpdateAvailable: true, UpdateSupported: true,
		}}, func() {}),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, projectRequest(http.MethodGet, "/api/v1/infrastructure/update", ""))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"currentVersion":"1.0.0"`) ||
		!strings.Contains(response.Body.String(), `"updateAvailable":true`) ||
		!strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("update check response = %d/%s headers=%v", response.Code, response.Body, response.Header())
	}
}

func TestSelfUpdateRespondsBeforeTriggeringDaemonShutdown(t *testing.T) {
	t.Parallel()
	shutdowns := 0
	handler := server.Handler(server.DefaultMeta("ready"),
		server.WithAdmission(admission.New()),
		server.WithSelfUpdate(updaterStub{result: selfupdate.Result{PreviousVersion: "1.0.0", TargetVersion: "2.0.0"}}, func() { shutdowns++ }),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, projectRequest(http.MethodPost, "/api/v1/infrastructure/update", ""))
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"targetVersion":"2.0.0"`) || shutdowns != 1 {
		t.Fatalf("update response = %d/%s shutdowns=%d", response.Code, response.Body, shutdowns)
	}
}

func TestSelfUpdateReturnsBoundedBusyDetails(t *testing.T) {
	t.Parallel()
	handler := server.Handler(server.DefaultMeta("ready"),
		server.WithAdmission(admission.New()),
		server.WithSelfUpdate(updaterStub{err: selfupdate.BusyError{Snapshot: admission.Snapshot{
			Blockers: []admission.Blocker{{Kind: "backup", ID: "backup-id"}}, Total: 1,
		}}}, func() { t.Fatal("busy update triggered shutdown") }),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, projectRequest(http.MethodPost, "/api/v1/infrastructure/update", ""))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"platform_busy"`) || !strings.Contains(response.Body.String(), `"id":"backup-id"`) {
		t.Fatalf("busy response = %d/%s", response.Code, response.Body)
	}

	handler = server.Handler(server.DefaultMeta("ready"),
		server.WithAdmission(admission.New()),
		server.WithSelfUpdate(updaterStub{err: errors.New("signature failed")}, func() {}),
	)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, projectRequest(http.MethodPost, "/api/v1/infrastructure/update", ""))
	if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), "signature") {
		t.Fatalf("failure response leaked detail = %d/%s", response.Code, response.Body)
	}
}

func TestSelfUpdateReportsAlreadyCurrentWithoutShutdown(t *testing.T) {
	t.Parallel()
	shutdowns := 0
	handler := server.Handler(server.DefaultMeta("ready"),
		server.WithAdmission(admission.New()),
		server.WithSelfUpdate(updaterStub{err: selfupdate.ErrUpToDate}, func() { shutdowns++ }),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, projectRequest(http.MethodPost, "/api/v1/infrastructure/update", ""))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"already_up_to_date"`) || shutdowns != 0 {
		t.Fatalf("up-to-date response = %d/%s shutdowns=%d", response.Code, response.Body, shutdowns)
	}
}
