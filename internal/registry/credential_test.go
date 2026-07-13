package registry

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
)

func TestCredentialLifecycleAndUploadCancellation(t *testing.T) {
	t.Parallel()
	fixture := newRegistryHTTPFixture(t)
	ctx := context.Background()
	actor := Actor{Kind: "access", ID: "user", Email: "admin@example.com"}
	pull, err := fixture.application.CreateCredential(ctx, CreateCredentialInput{
		RepositoryID: fixture.private.Repository.ID, Name: "reader", Permission: "pull", Actor: actor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.application.Authenticate(ctx, fixture.private.Repository.Name, pull.Username, pull.Secret, false); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.application.Authenticate(ctx, fixture.private.Repository.Name, pull.Username, pull.Secret, true); !errors.Is(err, ErrDenied) {
		t.Fatalf("pull credential write error = %v", err)
	}

	push, err := fixture.application.CreateCredential(ctx, CreateCredentialInput{
		RepositoryID: fixture.private.Repository.ID, Name: "writer", Permission: "pull_push", Actor: actor,
	})
	if err != nil {
		t.Fatal(err)
	}
	authentication, err := fixture.application.Authenticate(ctx, fixture.private.Repository.Name, push.Username, push.Secret, true)
	if err != nil {
		t.Fatal(err)
	}
	upload, err := fixture.application.BeginUpload(ctx, authentication)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.application.AppendUpload(ctx, authentication, upload.ID, bytes.NewBufferString("temporary")); err != nil {
		t.Fatal(err)
	}
	path, err := fixture.application.payloads.uploadPath(fixture.private.Repository.ID, upload.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.application.DeleteCredential(ctx, fixture.private.Repository.ID, push.Credential.ID, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("revoked credential upload survived: %v", err)
	}
	if _, err := fixture.application.Authenticate(ctx, fixture.private.Repository.Name, push.Username, push.Secret, false); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("revoked credential authentication error = %v", err)
	}
	credentials, err := fixture.application.Credentials(ctx, fixture.private.Repository.ID)
	if err != nil || len(credentials) != 2 {
		t.Fatalf("remaining credentials = %+v, %v", credentials, err)
	}
}
