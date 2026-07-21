//go:build integration

package managedimages

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDockerHubOfficialCatalog(t *testing.T) {
	if os.Getenv("PLATFORMD_DOCKER_HUB_INTEGRATION") != "1" {
		t.Skip("set PLATFORMD_DOCKER_HUB_INTEGRATION=1 to call the live Docker Hub API")
	}
	client, err := New("https://hub.docker.com", &http.Client{Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	for _, engine := range []Engine{PostgreSQL, Redis} {
		page, err := client.List(context.Background(), engine, 1, 2, "alpine")
		if err != nil {
			t.Fatalf("list %s tags: %v", engine, err)
		}
		if len(page.Tags) == 0 || page.Total < len(page.Tags) || page.Page != 1 || page.PageSize != 2 {
			t.Fatalf("%s page = %+v", engine, page)
		}
		for _, tag := range page.Tags {
			if !strings.Contains(tag.Name, "alpine") {
				t.Fatalf("%s search returned non-Alpine tag %q", engine, tag.Name)
			}
		}
	}
}
