package mcp

import (
	"encoding/json"
	"net/http"
)

const (
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

type requestMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type responseMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func writeRPCResult(response http.ResponseWriter, id json.RawMessage, result any) {
	writeRPC(response, responseMessage{JSONRPC: "2.0", ID: id, Result: result})
}

func writeRPCError(response http.ResponseWriter, id json.RawMessage, code int, message string) {
	writeRPCErrorStatus(response, id, http.StatusOK, code, message)
}

func writeRPCErrorStatus(response http.ResponseWriter, id json.RawMessage, status, code int, message string) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	writeRPCStatus(response, status, responseMessage{
		JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message},
	})
}

func writeRPC(response http.ResponseWriter, message responseMessage) {
	writeRPCStatus(response, http.StatusOK, message)
}

func writeRPCStatus(response http.ResponseWriter, status int, message responseMessage) {
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(message)
}
