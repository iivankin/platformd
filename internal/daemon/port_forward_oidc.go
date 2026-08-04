package daemon

import (
	"context"

	"github.com/iivankin/platformd/internal/imageupload"
	"github.com/iivankin/platformd/internal/portforward"
)

type portForwardOIDCVerifier struct {
	inner *imageupload.OIDCVerifier
}

func (verifier portForwardOIDCVerifier) Verify(
	ctx context.Context,
	token, audience, repository string,
	workflows []string,
) (portforward.OIDCIdentity, error) {
	identity, err := verifier.inner.Verify(ctx, imageupload.OIDCRequest{
		Token: token, Audience: audience, Repository: repository, Workflows: workflows,
	})
	if err != nil {
		return portforward.OIDCIdentity{}, err
	}
	return portforward.OIDCIdentity{Repository: identity.Repository, RunID: identity.RunID}, nil
}
