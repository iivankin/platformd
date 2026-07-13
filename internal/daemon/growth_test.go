package daemon

import "context"

type allowRuntimeGrowth struct{}

func (allowRuntimeGrowth) PermitGrowth(context.Context) error { return nil }
