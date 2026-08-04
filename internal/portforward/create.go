package portforward

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/serviceconfig"
)

var ErrOIDCUnauthorized = errors.New("port forward OIDC unauthorized")

type OIDCIdentity struct {
	Repository string
	RunID      string
}

type OIDCVerifier interface {
	Verify(ctx context.Context, token, audience, repository string, workflows []string) (OIDCIdentity, error)
}

type ResolvedResource struct {
	ID          string
	Kind        string
	Name        string
	PortForward *serviceconfig.PortForward
}

func (application *Application) CreateOIDC(
	ctx context.Context,
	input CreateInput,
	token string,
	audience string,
) (Grant, error) {
	if application.oidc == nil {
		return Grant{}, ErrOIDCUnauthorized
	}
	lifetime, err := validateCreateInput(input)
	if err != nil {
		return Grant{}, err
	}
	project, err := application.repository.ResolveProject(ctx, input.Project)
	if err != nil {
		return Grant{}, err
	}
	if project.ID == "" || project.Name != input.Project {
		return Grant{}, errors.New("resolved port forward project is invalid")
	}
	resource, err := application.repository.ResolveResource(ctx, project.ID, input.Resource)
	if err != nil {
		return Grant{}, err
	}
	switch resource.Kind {
	case "service", "postgres", "redis", "object_store":
		if resource.PortForward == nil || resource.PortForward.Repository == "" {
			return Grant{}, ErrOIDCUnauthorized
		}
	default:
		return Grant{}, ErrOIDCUnauthorized
	}
	if resource.ID == "" || resource.Name != input.Resource {
		return Grant{}, errors.New("resolved port forward resource is invalid")
	}
	identity, err := application.oidc.Verify(
		ctx, token, audience, resource.PortForward.Repository, resource.PortForward.Workflows,
	)
	if err != nil {
		return Grant{}, ErrOIDCUnauthorized
	}
	if _, err := application.resolver.ResolveResourceAddress(project.ID, resource.Kind, resource.ID, input.Port); err != nil {
		return Grant{}, fmt.Errorf("%w: %v", ErrTargetUnavailable, err)
	}
	actor := "oidc:" + strings.TrimSpace(identity.RunID)
	if actor == "oidc:" {
		actor = "oidc:" + strings.TrimSpace(identity.Repository)
	}
	return application.issueTicket(ctx, project, resource, input.Port, lifetime, actor)
}

func (application *Application) Create(ctx context.Context, identity automation.Identity, input CreateInput) (Grant, error) {
	if !identity.IsAdmin() {
		return Grant{}, automation.ErrAdminRequired
	}
	lifetime, err := validateCreateInput(input)
	if err != nil {
		return Grant{}, err
	}
	project, err := application.repository.ResolveProject(ctx, input.Project)
	if err != nil {
		return Grant{}, err
	}
	if project.ID == "" || project.Name != input.Project {
		return Grant{}, errors.New("resolved port forward project is invalid")
	}
	if !identity.AllowsProject(project.ID) {
		return Grant{}, automation.ErrProjectBoundary
	}
	resource, err := application.repository.ResolveResource(ctx, project.ID, input.Resource)
	if err != nil {
		return Grant{}, err
	}
	switch resource.Kind {
	case "service", "postgres", "redis", "object_store":
	default:
		return Grant{}, ErrInvalidInput
	}
	if resource.ID == "" || resource.Name != input.Resource {
		return Grant{}, errors.New("resolved port forward resource is invalid")
	}
	if _, err := application.resolver.ResolveResourceAddress(project.ID, resource.Kind, resource.ID, input.Port); err != nil {
		return Grant{}, fmt.Errorf("%w: %v", ErrTargetUnavailable, err)
	}
	return application.issueTicket(ctx, project, resource, input.Port, lifetime, identity.TokenID)
}
