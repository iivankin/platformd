package portforward

import (
	"fmt"
	"net/url"

	"github.com/iivankin/platformd/internal/portforwardprotocol"
)

const (
	EndpointPath       = portforwardprotocol.EndpointPath
	WebSocketProtocol  = portforwardprotocol.WebSocketProtocol
	InstallerURL       = "https://raw.githubusercontent.com/iivankin/platformd/main/install.sh"
	ReleaseDownloadURL = "https://github.com/iivankin/platformd/releases/latest"
)

type Instructions struct {
	InstallerURL   string `json:"installerUrl"`
	ReleaseURL     string `json:"releaseUrl"`
	InstallCommand string `json:"installCommand"`
	ConnectCommand string `json:"connectCommand"`
	WebSocketURL   string `json:"websocketUrl"`
}

func ConnectionInstructions(hostname, ticket string, localPort int) Instructions {
	websocketURL := (&url.URL{Scheme: "wss", Host: hostname, Path: EndpointPath}).String()
	return Instructions{
		InstallerURL:   InstallerURL,
		ReleaseURL:     ReleaseDownloadURL,
		InstallCommand: "curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 " + InstallerURL + " | sh -s -- forward",
		ConnectCommand: fmt.Sprintf(
			"PLATFORMD_FORWARD_TICKET='%s' platformd-forward --url '%s' --local-port %d",
			ticket, websocketURL, localPort,
		),
		WebSocketURL: websocketURL,
	}
}
