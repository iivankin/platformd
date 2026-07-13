package daemon

import (
	"errors"
	"time"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/cgrouptree"
	"github.com/iivankin/platformd/internal/rootexec"
	"github.com/iivankin/platformd/internal/state"
)

const (
	serverExecCommandBytes    = 64 << 10
	serverExecOutputBytes     = 1 << 20
	serverExecDefaultTimeout  = 30 * time.Second
	serverExecMaximumTimeout  = 5 * time.Minute
	serverExecMaximumParallel = 4
)

func newServerExecApplication(cgroups *cgrouptree.Tree, store *state.Store) (*automation.ServerExecApplication, error) {
	if cgroups == nil || store == nil {
		return nil, errors.New("server exec daemon dependencies are incomplete")
	}
	runner, err := rootexec.New(rootexec.Config{
		CreateLeaf: func(identifier string) (rootexec.Leaf, error) {
			return cgroups.CreateLeaf(identifier)
		},
		CommandBytes: serverExecCommandBytes, OutputBytes: serverExecOutputBytes,
		DefaultTimeout: serverExecDefaultTimeout, MaximumTimeout: serverExecMaximumTimeout,
		MaximumParallel: serverExecMaximumParallel,
	})
	if err != nil {
		return nil, err
	}
	return automation.NewServerExecApplication(runner, store, nil, nil)
}
