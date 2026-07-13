package managedredis

import "context"

type allowGrowthGate struct{}

func (allowGrowthGate) PermitGrowth(context.Context) error { return nil }
