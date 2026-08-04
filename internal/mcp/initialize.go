package mcp

import (
	"encoding/json"
	"net/http"
)

func (handler *Handler) initialize(response http.ResponseWriter, message requestMessage) {
	if len(message.ID) == 0 {
		writeRPCError(response, nil, codeInvalidRequest, "initialize requires an id")
		return
	}
	var params struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ClientInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"clientInfo"`
	}
	if err := json.Unmarshal(message.Params, &params); err != nil || params.ProtocolVersion == "" || params.Capabilities == nil || params.ClientInfo.Name == "" || params.ClientInfo.Version == "" {
		writeRPCError(response, message.ID, codeInvalidParams, "Invalid initialize params")
		return
	}
	protocolVersion := ProtocolVersion
	if supportsProtocolVersion(params.ProtocolVersion) {
		protocolVersion = params.ProtocolVersion
	}
	writeRPCResult(response, message.ID, map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]string{
			"name": "platformd", "version": handler.version,
		},
	})
}
