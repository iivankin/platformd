package server

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"runtime"
	"strings"

	"github.com/iivankin/platformd/internal/ui"
	"github.com/iivankin/platformd/internal/version"
)

type Meta struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
	Status       string `json:"status"`
	Version      string `json:"version"`
}

func Handler() http.Handler {
	static := newSPAHandler(ui.Files())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealth)
	mux.HandleFunc("GET /api/v1/meta", handleMeta)
	mux.Handle("/", static)
	return securityHeaders(mux)
}

func handleHealth(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write([]byte("ok\n"))
}

func handleMeta(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(Meta{
		Architecture: runtime.GOARCH,
		OS:           runtime.GOOS,
		Status:       "bootstrapping",
		Version:      version.Version,
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; connect-src 'self' wss:; font-src 'self'; frame-ancestors 'none'; img-src 'self' data:; object-src 'none'; script-src 'self'; style-src 'self'")
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(response, request)
	})
}

type spaHandler struct {
	files      fs.FS
	fileServer http.Handler
	index      []byte
}

func newSPAHandler(files fs.FS) http.Handler {
	index, err := fs.ReadFile(files, "index.html")
	if err != nil {
		panic("read embedded UI index: " + err.Error())
	}
	return &spaHandler{files: files, fileServer: http.FileServerFS(files), index: index}
}

func (handler *spaHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		http.Error(response, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(request.URL.Path, "/")
	if path == "" || path == "index.html" {
		handler.serveIndex(response, request)
		return
	}
	if _, err := fs.Stat(handler.files, path); err == nil {
		response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		handler.fileServer.ServeHTTP(response, request)
		return
	}

	if strings.HasPrefix(request.URL.Path, "/api/") {
		http.NotFound(response, request)
		return
	}

	handler.serveIndex(response, request)
}

func (handler *spaHandler) serveIndex(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Content-Length", fmt.Sprintf("%d", len(handler.index)))
	if request.Method == http.MethodHead {
		return
	}
	_, _ = response.Write(handler.index)
}
