package imageupload

import (
	"testing"
	"time"
)

func TestOIDCClaimsAuthorizeRepositoryBranchWorkflowAndAudience(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	verifier := NewOIDCVerifier(nil, func() time.Time { return now })
	claims := oidcClaims{
		Audience: oidcAudience{"https://platform.example.com/public/api/v1/projects/shop/services/api/image"},
		Issuer:   githubIssuer, ExpiresAt: now.Add(5 * time.Minute).Unix(), IssuedAt: now.Unix(),
		Repository: "acme/backend", Ref: "refs/heads/main", SHA: "commit",
		Workflow: "Deploy", WorkflowRef: "acme/backend/.github/workflows/deploy.yml@refs/heads/main",
		Actor: "developer", RunID: "123", RunAttempt: "1",
	}
	request := OIDCRequest{
		Audience: claims.Audience[0], Repository: "acme/backend", Branch: "main",
		Workflows: []string{"deploy.yml"}, Production: true,
	}
	if err := verifier.validateClaims(claims, request); err != nil {
		t.Fatalf("valid claims rejected: %v", err)
	}

	for name, mutate := range map[string]func(*oidcClaims, *OIDCRequest){
		"audience":   func(_ *oidcClaims, value *OIDCRequest) { value.Audience += "/other" },
		"repository": func(_ *oidcClaims, value *OIDCRequest) { value.Repository = "acme/other" },
		"branch":     func(value *oidcClaims, _ *OIDCRequest) { value.Ref = "refs/heads/feature" },
		"workflow": func(value *oidcClaims, _ *OIDCRequest) {
			value.WorkflowRef = "acme/backend/.github/workflows/other.yml@refs/heads/main"
		},
		"run": func(value *oidcClaims, _ *OIDCRequest) { value.RunID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			changedClaims, changedRequest := claims, request
			mutate(&changedClaims, &changedRequest)
			if err := verifier.validateClaims(changedClaims, changedRequest); err == nil {
				t.Fatal("invalid claims were accepted")
			}
		})
	}
}

func TestOIDCPreviewAcceptsNonProductionRef(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	verifier := NewOIDCVerifier(nil, func() time.Time { return now })
	claims := oidcClaims{
		Audience: oidcAudience{"https://platform.example.com/upload"}, Issuer: githubIssuer,
		ExpiresAt: now.Add(time.Minute).Unix(), IssuedAt: now.Unix(), Repository: "acme/backend",
		Ref: "refs/pull/42/merge", SHA: "commit", WorkflowRef: "acme/backend/.github/workflows/preview.yaml@refs/pull/42/merge",
		Actor: "developer", RunID: "123", RunAttempt: "1",
	}
	if err := verifier.validateClaims(claims, OIDCRequest{
		Audience: claims.Audience[0], Repository: claims.Repository, Branch: "main",
		Workflows: []string{"preview.yaml"}, Production: false,
	}); err != nil {
		t.Fatalf("preview claims rejected: %v", err)
	}
}
