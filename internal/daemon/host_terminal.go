package daemon

import (
	"errors"
	"time"

	"github.com/iivankin/platformd/internal/cgrouptree"
	"github.com/iivankin/platformd/internal/hostterminal"
	"github.com/iivankin/platformd/internal/state"
)

const (
	serverTerminalIdleTimeout      = 30 * time.Minute
	serverTerminalAbsoluteLifetime = 8 * time.Hour
)

func newHostTerminalApplication(cgroups *cgrouptree.Tree, store *state.Store, installationID string) (*hostterminal.Application, error) {
	if cgroups == nil || store == nil || installationID == "" {
		return nil, errors.New("host terminal daemon dependencies are incomplete")
	}
	return hostterminal.New(hostterminal.Config{
		Audit: store, InstallationID: installationID,
		CreateLeaf: func(identifier string) (hostterminal.Leaf, error) {
			return cgroups.CreateOperationLeaf(identifier)
		},
	})
}
