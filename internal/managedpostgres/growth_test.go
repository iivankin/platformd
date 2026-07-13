package managedpostgres

import (
	"context"
	"net/netip"
)

type allowGrowthGate struct{}

func (allowGrowthGate) PermitGrowth(context.Context) error { return nil }

type allowMaintenanceGate struct{}

func (allowMaintenanceGate) BlockDatabase(context.Context, string, netip.Addr, uint16) (func() error, error) {
	return func() error { return nil }, nil
}
