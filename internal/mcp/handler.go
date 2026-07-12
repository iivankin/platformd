package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/managedimages"
)

const (
	ProtocolVersion  = "2025-11-25"
	maximumBodyBytes = 1 << 20
)

type Handler struct {
	hostname   string
	version    string
	repository Repository
	services   *automation.ServiceApplication
	logs       *automation.LogApplication
	images     ManagedImageCatalog
	tools      []Tool
}

type Config struct {
	Hostname   string
	Version    string
	Repository Repository
	Services   *automation.ServiceApplication
	Logs       *automation.LogApplication
	Images     ManagedImageCatalog
}

type ManagedImageCatalog interface {
	List(context.Context, managedimages.Engine, int, int) (managedimages.Page, error)
}

func New(config Config) (*Handler, error) {
	if config.Hostname == "" || config.Version == "" || config.Repository == nil || config.Services == nil || config.Logs == nil || config.Images == nil {
		return nil, errors.New("MCP handler dependencies are incomplete")
	}
	return &Handler{
		hostname: config.Hostname, version: config.Version, repository: config.Repository,
		services: config.Services, logs: config.Logs, images: config.Images,
		tools: readTools(),
	}, nil
}

func (handler *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "private, no-store")
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	origins := request.Header.Values("Origin")
	if len(origins) > 1 || (len(origins) == 1 && origins[0] != "https://"+handler.hostname) {
		http.Error(response, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	if !acceptsMCP(request.Header.Values("Accept")) {
		http.Error(response, http.StatusText(http.StatusNotAcceptable), http.StatusNotAcceptable)
		return
	}
	mediaType, parameters, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	charset := strings.ToLower(parameters["charset"])
	if err != nil || mediaType != "application/json" || (charset != "" && charset != "utf-8") {
		http.Error(response, http.StatusText(http.StatusUnsupportedMediaType), http.StatusUnsupportedMediaType)
		return
	}
	identity, ok := automation.IdentityFromContext(request.Context())
	if !ok {
		http.Error(response, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, maximumBodyBytes)
	decoder := json.NewDecoder(request.Body)
	var message requestMessage
	if err := decoder.Decode(&message); err != nil || requireEnd(decoder) != nil || message.JSONRPC != "2.0" || message.Method == "" {
		writeRPCError(response, nil, codeInvalidRequest, "Invalid Request")
		return
	}
	versionHeaders := request.Header.Values("MCP-Protocol-Version")
	if message.Method == "initialize" {
		if len(versionHeaders) > 1 || (len(versionHeaders) == 1 && versionHeaders[0] != ProtocolVersion) {
			http.Error(response, "Unsupported MCP protocol version", http.StatusBadRequest)
			return
		}
	} else if len(versionHeaders) != 1 || versionHeaders[0] != ProtocolVersion {
		http.Error(response, "MCP-Protocol-Version must be "+ProtocolVersion, http.StatusBadRequest)
		return
	}

	switch message.Method {
	case "initialize":
		handler.initialize(response, message)
	case "notifications/initialized":
		if len(message.ID) != 0 {
			writeRPCError(response, message.ID, codeInvalidRequest, "Initialized must be a notification")
			return
		}
		response.Header().Set("Cache-Control", "private, no-store")
		response.WriteHeader(http.StatusAccepted)
	case "tools/list":
		if len(message.ID) == 0 {
			writeRPCError(response, nil, codeInvalidRequest, "tools/list requires an id")
			return
		}
		handler.listTools(response, message, identity)
	case "tools/call":
		if len(message.ID) == 0 {
			writeRPCError(response, nil, codeInvalidRequest, "tools/call requires an id")
			return
		}
		handler.callTool(response, request, message, identity)
	default:
		if len(message.ID) == 0 {
			http.Error(response, "Unsupported MCP notification", http.StatusBadRequest)
			return
		}
		writeRPCError(response, message.ID, codeMethodNotFound, "Method not found")
	}
}

func acceptsMCP(values []string) bool {
	jsonAccepted := false
	sseAccepted := false
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			mediaType, parameters, err := mime.ParseMediaType(strings.TrimSpace(part))
			if err != nil {
				continue
			}
			if quality, exists := parameters["q"]; exists {
				parsed, parseErr := strconv.ParseFloat(quality, 64)
				if parseErr != nil || parsed <= 0 {
					continue
				}
			}
			jsonAccepted = jsonAccepted || mediaType == "application/json"
			sseAccepted = sseAccepted || mediaType == "text/event-stream"
		}
	}
	return jsonAccepted && sseAccepted
}

func requireEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("unexpected JSON content")
	}
	return nil
}
