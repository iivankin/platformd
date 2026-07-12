package automation

import "context"

type Identity struct {
	TokenID   string
	Role      string
	ProjectID *string
}

type identityKey struct{}

func WithIdentity(ctx context.Context, identity Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, identity)
}

func IdentityFromContext(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityKey{}).(Identity)
	return identity, ok
}

func (identity Identity) AllowsProject(projectID string) bool {
	return identity.ProjectID == nil || *identity.ProjectID == projectID
}

func (identity Identity) IsAdmin() bool {
	return identity.Role == "admin"
}
