package hosthub

import (
	"net/http"
	"os"
	"strings"

	"github.com/iivankin/platformd/internal/hostconn"
	"github.com/iivankin/platformd/internal/hosttoken"
)

func (hub *Hub) ImageHandler() http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "private, no-store")
		if request.Method != http.MethodGet {
			response.Header().Set("Allow", http.MethodGet)
			http.Error(response, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		token, ok := bearerHostToken(request.Header.Values("Authorization"))
		if !ok {
			response.Header().Set("WWW-Authenticate", `Bearer realm="platformd-host"`)
			http.Error(response, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		hostID, secret, err := hosttoken.ParseHost(token)
		if err != nil {
			http.Error(response, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		stored, err := hub.store.HostCredential(request.Context(), hostID)
		if err != nil || !hosttoken.Verify("host", hostID, secret, stored.TokenHMAC) {
			http.Error(response, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		revisionID := strings.TrimPrefix(request.URL.Path, hostconn.ImagePathPrefix)
		if revisionID == "" || strings.Contains(revisionID, "/") {
			http.NotFound(response, request)
			return
		}
		revision, err := hub.store.ImageRevisionAssignedToHost(request.Context(), hostID, revisionID)
		if err != nil {
			http.NotFound(response, request)
			return
		}
		file, err := os.Open(revision.ArchivePath)
		if err != nil {
			http.Error(response, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			http.Error(response, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}
		response.Header().Set("Content-Type", "application/octet-stream")
		http.ServeContent(response, request, revisionID+".oci", info.ModTime(), file)
	})
}
