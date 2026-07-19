package server

import (
	"context"
	"github.com/iivankin/platformd/internal/state"
)

type ServiceImageCredentialInput struct {
	ServiceID       string
	ImageReference  string
	Username        string
	Password        string
	UpdatedAtMillis int64
}

type ServiceImageCredentialManager interface {
	PrepareServiceImageCredential(context.Context, ServiceImageCredentialInput) (*state.ServiceImageCredential, error)
	RevealServiceImageCredential(context.Context, string) (string, string, string, error)
}
