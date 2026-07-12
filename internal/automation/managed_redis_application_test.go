package automation

import (
	"context"
	"testing"

	"github.com/iivankin/platformd/internal/managedredis"
)

type redisCreator struct{ input managedredis.CreateInput }

func (creator *redisCreator) Create(_ context.Context, input managedredis.CreateInput) (managedredis.CreateResult, error) {
	creator.input = input
	return managedredis.CreateResult{Password: "password"}, nil
}

func TestManagedRedisAutomationRequiresAdminAndProjectBoundary(t *testing.T) {
	t.Parallel()
	creator := &redisCreator{}
	application, err := NewManagedRedisApplication(creator)
	if err != nil {
		t.Fatal(err)
	}
	project := "project"
	if _, err := application.Create(context.Background(), Identity{TokenID: "read", Role: "read"}, CreateManagedRedisInput{ProjectID: project}); err != ErrAdminRequired {
		t.Fatalf("read token error = %v", err)
	}
	other := "other"
	if _, err := application.Create(context.Background(), Identity{TokenID: "admin", Role: "admin", ProjectID: &other}, CreateManagedRedisInput{ProjectID: project}); err != ErrProjectBoundary {
		t.Fatalf("cross-project error = %v", err)
	}
	if _, err := application.Create(context.Background(), Identity{TokenID: "admin", Role: "admin", ProjectID: &project}, CreateManagedRedisInput{
		ProjectID: project, Name: "cache", ImageTag: "7.4",
	}); err != nil {
		t.Fatal(err)
	}
	if creator.input.Actor != (managedredis.Actor{Kind: "token", ID: "admin"}) {
		t.Fatalf("creator actor = %+v", creator.input.Actor)
	}
}
