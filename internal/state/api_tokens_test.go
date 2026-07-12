package state_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

func TestAPITokenCreateListAndRevokeKeepsSecretVerifierPrivate(t *testing.T) {
	store, err := state.Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateProject(context.Background(), state.CreateProject{
		ID: "project", Name: "shop", AuditEventID: "project-audit", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	projectID := "project"
	created, err := store.CreateAPIToken(context.Background(), state.CreateAPIToken{
		APIToken: state.APIToken{
			ID: "token", Name: "automation", Role: "admin", ProjectID: &projectID,
			SecretHMAC: bytes.Repeat([]byte{0x42}, 32), CreatedAtMillis: 2,
		},
		AuditEventID: "create-audit", ActorID: "actor", ActorEmail: "admin@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.SecretHMAC != nil {
		t.Fatal("created API token exposed its secret verifier")
	}
	tokens, err := store.APITokens(context.Background())
	if err != nil || len(tokens) != 1 || tokens[0].ProjectID == nil || *tokens[0].ProjectID != projectID || tokens[0].SecretHMAC != nil {
		t.Fatalf("listed API tokens = %+v, %v", tokens, err)
	}
	if err := store.RevokeAPIToken(context.Background(), state.RevokeAPIToken{
		ID: "token", AuditEventID: "revoke-audit", ActorID: "actor",
		ActorEmail: "admin@example.com", RevokedAtMillis: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeAPIToken(context.Background(), state.RevokeAPIToken{
		ID: "token", AuditEventID: "second-revoke-audit", ActorID: "actor",
		ActorEmail: "admin@example.com", RevokedAtMillis: 4,
	}); !errors.Is(err, state.ErrAPITokenNotFound) {
		t.Fatalf("second revoke error = %v", err)
	}
	var auditCount int
	if err := store.QueryRowContext(context.Background(), `
SELECT count(*) FROM audit_events WHERE target_id = 'token' AND action IN ('api_token.create', 'api_token.revoke')`).Scan(&auditCount); err != nil || auditCount != 2 {
		t.Fatalf("token audit count = %d, %v", auditCount, err)
	}
}
