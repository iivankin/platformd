package automation

import (
	"context"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/objectstore"
)

type ObjectStoreCreator interface {
	Create(context.Context, objectstore.CreateInput) (objectstore.CreateResult, error)
}

type ObjectStoreApplication struct {
	creator ObjectStoreCreator
}

type CreateObjectStoreInput struct {
	ProjectID            string
	Name                 string
	BucketName           string
	PublicHostname       string
	CORSOrigins          []string
	CredentialName       string
	CredentialPermission string
}

func NewObjectStoreApplication(creator ObjectStoreCreator) (*ObjectStoreApplication, error) {
	if creator == nil {
		return nil, errors.New("object store automation creator is required")
	}
	return &ObjectStoreApplication{creator: creator}, nil
}

func (application *ObjectStoreApplication) Create(ctx context.Context, identity Identity, input CreateObjectStoreInput) (objectstore.CreateResult, error) {
	if err := authorizeServiceMutation(identity, input.ProjectID); err != nil {
		return objectstore.CreateResult{}, err
	}
	if input.Name == "" {
		return objectstore.CreateResult{}, fmt.Errorf("%w: name is required", ErrInvalidInput)
	}
	return application.creator.Create(ctx, objectstore.CreateInput{
		ProjectID: input.ProjectID, Name: input.Name, BucketName: input.BucketName,
		PublicHostname: input.PublicHostname, CORSOrigins: input.CORSOrigins,
		CredentialName: input.CredentialName, CredentialPermission: input.CredentialPermission,
		Actor: objectstore.Actor{Kind: "token", ID: identity.TokenID},
	})
}
