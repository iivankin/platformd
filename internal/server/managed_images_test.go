package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/managedimages"
	"github.com/iivankin/platformd/internal/server"
)

type managedImageCatalog struct {
	engine   managedimages.Engine
	page     int
	pageSize int
}

func (catalog *managedImageCatalog) List(_ context.Context, engine managedimages.Engine, page, pageSize int) (managedimages.Page, error) {
	catalog.engine = engine
	catalog.page = page
	catalog.pageSize = pageSize
	return managedimages.Page{Tags: []managedimages.Tag{{Name: "18.3"}}, Page: page, PageSize: pageSize}, nil
}

func TestManagedImageTagsRequireAccessAndUseBoundedPage(t *testing.T) {
	catalog := &managedImageCatalog{}
	direct := server.Handler(server.DefaultMeta("ready"), server.WithManagedImages(catalog))
	response := httptest.NewRecorder()
	direct.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/managed-images/postgres/tags", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated tags = %d/%s", response.Code, response.Body)
	}
	handler := access.ProtectAdmin("admin.example.com", projectVerifier{}, direct)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, projectRequest(http.MethodGet, "/api/v1/managed-images/postgres/tags?page=2&pageSize=25&search=18", ""))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"18.3"`) || catalog.engine != managedimages.PostgreSQL || catalog.page != 2 || catalog.pageSize != 25 {
		t.Fatalf("tags response = %d/%s catalog=%+v", response.Code, response.Body, catalog)
	}
}
