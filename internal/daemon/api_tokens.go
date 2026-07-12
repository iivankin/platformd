package daemon

import (
	"context"

	"github.com/iivankin/platformd/internal/apitoken"
	"github.com/iivankin/platformd/internal/state"
)

type liveAPITokenRepository struct {
	store    *state.Store
	verifier apitoken.Verifier
}

func (repository liveAPITokenRepository) APITokens(ctx context.Context) ([]state.APIToken, error) {
	return repository.store.APITokens(ctx)
}

func (repository liveAPITokenRepository) CreateAPIToken(ctx context.Context, input state.CreateAPIToken, secret string) (state.APIToken, error) {
	input.SecretHMAC = repository.verifier.Digest(input.ID, secret)
	return repository.store.CreateAPIToken(ctx, input)
}

func (repository liveAPITokenRepository) RevokeAPIToken(ctx context.Context, input state.RevokeAPIToken) error {
	return repository.store.RevokeAPIToken(ctx, input)
}
