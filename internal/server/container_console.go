package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/containerconsole"
	"github.com/iivankin/platformd/internal/terminaltransport"
)

type ContainerConsole interface {
	Open(context.Context, containerconsole.OpenInput) (terminaltransport.Session, error)
	Shells(context.Context, string, string) ([]string, error)
}

func registerContainerConsoleRoute(mux *http.ServeMux, hostname string, application ContainerConsole, gate *admission.Gate) error {
	handler, err := terminaltransport.New(hostname, func(ctx context.Context, open terminaltransport.OpenRequest, size terminaltransport.Size) (terminaltransport.Session, error) {
		command, err := containerTerminalCommand(open.HTTP)
		if err != nil {
			return nil, err
		}
		sourceIP, err := terminalSourceIP(open.HTTP)
		if err != nil {
			return nil, err
		}
		return application.Open(ctx, containerconsole.OpenInput{
			ProjectID: open.HTTP.PathValue("projectID"), ServiceID: open.HTTP.PathValue("serviceID"),
			Command: command, SourceIP: sourceIP,
			Actor: containerconsole.Actor{ID: open.Identity.Subject, Email: open.Identity.Email}, Size: size,
		})
	}, 0, 0)
	if err != nil {
		return err
	}
	if gate != nil {
		handler.SetAdmission(func(request *http.Request) (func(), error) {
			lease, err := gate.Begin("container_console", request.PathValue("serviceID"))
			if err != nil {
				return nil, err
			}
			return lease.Release, nil
		})
	}
	mux.Handle("GET /api/v1/projects/{projectID}/services/{serviceID}/terminal", handler)
	mux.HandleFunc("GET /api/v1/projects/{projectID}/services/{serviceID}/terminal/shells", func(response http.ResponseWriter, request *http.Request) {
		if _, ok := access.IdentityFromContext(request.Context()); !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		shells, err := application.Shells(request.Context(), request.PathValue("projectID"), request.PathValue("serviceID"))
		if err != nil {
			writeAPIError(response, http.StatusConflict, "terminal_shell_probe_failed", "Unable to inspect terminal shells")
			return
		}
		writeJSON(response, http.StatusOK, struct {
			Shells []string `json:"shells"`
		}{Shells: shells})
	})
	return nil
}

func containerTerminalCommand(request *http.Request) ([]string, error) {
	query := request.URL.Query()
	arguments, explicit := query["arg"]
	shell := query.Get("shell")
	if explicit {
		if shell != "" || len(arguments) == 0 || len(arguments) > 64 {
			return nil, errors.New("explicit terminal command cannot be combined with shell")
		}
		result := make([]string, len(arguments))
		for index, argument := range arguments {
			if argument == "" {
				return nil, errors.New("terminal command contains an empty argument")
			}
			result[index] = argument
		}
		return result, nil
	}
	switch shell {
	case "", "sh":
		return []string{"/bin/sh"}, nil
	case "bash":
		return []string{"/bin/bash"}, nil
	default:
		return nil, errors.New("terminal shell must be sh or bash")
	}
}

func terminalSourceIP(request *http.Request) (string, error) {
	forwarded := request.Header.Values("Cf-Connecting-Ip")
	if len(forwarded) > 0 {
		if len(forwarded) != 1 || strings.Contains(forwarded[0], ",") {
			return "", errors.New("invalid Cloudflare source IP header")
		}
		address, err := netip.ParseAddr(strings.TrimSpace(forwarded[0]))
		if err != nil {
			return "", errors.New("invalid Cloudflare source IP")
		}
		return address.String(), nil
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return "", errors.New("invalid terminal source address")
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return "", errors.New("invalid terminal source IP")
	}
	return address.String(), nil
}
