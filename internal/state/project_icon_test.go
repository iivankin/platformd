package state_test

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

func pngIcon(t *testing.T, size int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestProjectIconRoundTrip(t *testing.T) {
	store, err := state.Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	created, err := store.CreateProject(ctx, state.CreateProject{
		ID: "project", Name: "shop", AuditEventID: "create",
		ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.HasIcon {
		t.Fatal("expected no icon on create")
	}
	payload := pngIcon(t, 32)
	updated, err := store.SetProjectIcon(ctx, state.SetProjectIconInput{
		ProjectID: "project", Bytes: payload, AuditEventID: "set",
		ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com", UpdatedAtMillis: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.HasIcon || updated.UpdatedAtMillis != 2 {
		t.Fatalf("updated = %+v", updated)
	}
	icon, err := store.ProjectIcon(ctx, "project")
	if err != nil {
		t.Fatal(err)
	}
	if icon.ContentType != "image/png" || !bytes.Equal(icon.Bytes, payload) {
		t.Fatalf("icon = %#v", icon)
	}
	cleared, err := store.ClearProjectIcon(ctx, state.ClearProjectIconInput{
		ProjectID: "project", AuditEventID: "clear",
		ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com", UpdatedAtMillis: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.HasIcon || cleared.UpdatedAtMillis != 3 {
		t.Fatalf("cleared = %+v", cleared)
	}
	if _, err := store.ProjectIcon(ctx, "project"); err != state.ErrProjectIconNotFound {
		t.Fatalf("after clear err = %v", err)
	}
}

func TestDetectProjectIconRejectsInvalidPayload(t *testing.T) {
	if _, err := state.DetectProjectIconContentType([]byte("not-an-image")); err != state.ErrInvalidProjectIcon {
		t.Fatalf("err = %v", err)
	}
	oversized := make([]byte, state.MaximumProjectIconBytes+1)
	copy(oversized, pngIcon(t, 8))
	if _, err := state.DetectProjectIconContentType(oversized); err != state.ErrInvalidProjectIcon {
		t.Fatalf("oversized err = %v", err)
	}
}
