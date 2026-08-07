package automation

import (
	"context"
	"errors"

	"github.com/iivankin/platformd/internal/journallogs"
)

type InfrastructureLogReader interface {
	Read(context.Context, journallogs.Query) (journallogs.Window, error)
}

type InfrastructureLogApplication struct {
	reader InfrastructureLogReader
}

type ReadInfrastructureLogsInput struct {
	Limit        int
	BeforeCursor string
}

func NewInfrastructureLogApplication(reader InfrastructureLogReader) (*InfrastructureLogApplication, error) {
	if reader == nil {
		return nil, errors.New("infrastructure log reader is required")
	}
	return &InfrastructureLogApplication{reader: reader}, nil
}

func (application *InfrastructureLogApplication) Read(ctx context.Context, identity Identity, input ReadInfrastructureLogsInput) (journallogs.Window, error) {
	if err := requireReadIdentity(identity); err != nil {
		return journallogs.Window{}, err
	}
	return application.reader.Read(ctx, journallogs.Query{Limit: input.Limit, BeforeCursor: input.BeforeCursor})
}
