package githubapp

import (
	"errors"
	"strings"
	"testing"
)

func TestParsePushReturnsExactCommitAndChangedPaths(t *testing.T) {
	revision := strings.Repeat("a", 40)
	event, err := ParsePush([]byte(`{
		"ref":"refs/heads/main",
		"after":"` + revision + `",
		"repository":{"id":42},
		"commits":[
			{"added":["Dockerfile"],"modified":["app/main.go"],"removed":[]},
			{"added":[],"modified":["app/main.go"],"removed":["old.txt"]}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if event.RepositoryID != 42 || event.Branch != "main" || event.Revision != revision {
		t.Fatalf("event identity = %+v", event)
	}
	if strings.Join(event.ChangedPaths, ",") != "Dockerfile,app/main.go,old.txt" {
		t.Fatalf("changed paths = %v", event.ChangedPaths)
	}
}

func TestParsePushIgnoresDeletedBranch(t *testing.T) {
	_, err := ParsePush([]byte(`{
		"ref":"refs/heads/old",
		"after":"0000000000000000000000000000000000000000",
		"repository":{"id":42},
		"commits":[]
	}`))
	if !errors.Is(err, ErrWebhookEventIgnored) {
		t.Fatalf("deleted branch error = %v", err)
	}
}

func TestParseCheckEventOnlyAcceptsCompletedChecks(t *testing.T) {
	revision := strings.Repeat("b", 40)
	payload := func(action string) []byte {
		return []byte(`{
			"action":"` + action + `",
			"repository":{"id":42},
			"check_suite":{"head_branch":"main","head_sha":"` + revision + `"}
		}`)
	}
	if _, err := ParseCheckEvent(payload("in_progress")); !errors.Is(err, ErrWebhookEventIgnored) {
		t.Fatalf("in-progress check error = %v", err)
	}
	event, err := ParseCheckEvent(payload("completed"))
	if err != nil {
		t.Fatal(err)
	}
	if !event.ChecksEvent || event.Branch != "main" || event.Revision != revision {
		t.Fatalf("completed check event = %+v", event)
	}
}

func TestParsePullRequestMapsLifecycleActions(t *testing.T) {
	revision := strings.Repeat("c", 40)
	payload := func(action string) []byte {
		return []byte(`{"action":"` + action + `","number":19,"repository":{"id":42},"pull_request":{"base":{"ref":"main"},"head":{"sha":"` + revision + `"}}}`)
	}
	event, err := ParsePullRequest(payload("synchronize"))
	if err != nil {
		t.Fatal(err)
	}
	if event.Action != "deploy" || event.Number != 19 || event.BaseBranch != "main" || event.Revision != revision {
		t.Fatalf("deploy event = %+v", event)
	}
	event, err = ParsePullRequest(payload("closed"))
	if err != nil {
		t.Fatal(err)
	}
	if event.Action != "close" {
		t.Fatalf("close event = %+v", event)
	}
}
