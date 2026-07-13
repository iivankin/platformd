package daemon

import (
	"errors"

	"github.com/iivankin/platformd/internal/ingress"
)

type liveAutomationRoute struct {
	factory *automationHandlerFactory
	router  *ingress.Router
}

func (route *liveAutomationRoute) Prepare(hostname string) (func() error, error) {
	handler, err := route.factory.Build(hostname)
	if err != nil {
		return nil, err
	}
	return func() error {
		if route.router == nil {
			return errors.New("automation ingress router is not configured")
		}
		return route.router.ReloadAutomation(hostname, handler)
	}, nil
}
