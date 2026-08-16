package daemon

import (
	"encoding/json"
	"testing"
)

func TestAttachIssueProjectIDsCopiesServiceProject(t *testing.T) {
	t.Parallel()
	body, err := attachIssueProjectIDs(
		[]byte(`{"data":[{"id":"issue","serviceId":"svc","title":"boom"}],"total":1}`),
		map[string]string{"svc": "project"},
	)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Data []struct {
			ID        string `json:"id"`
			ProjectID string `json:"projectId"`
			ServiceID string `json:"serviceId"`
			Title     string `json:"title"`
		} `json:"data"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Total != 1 || len(envelope.Data) != 1 ||
		envelope.Data[0].ID != "issue" || envelope.Data[0].Title != "boom" ||
		envelope.Data[0].ServiceID != "svc" || envelope.Data[0].ProjectID != "project" {
		t.Fatalf("annotated issues = %s", body)
	}
}
