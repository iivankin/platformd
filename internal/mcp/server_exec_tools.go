package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/rootexec"
)

func serverExecTool() Tool {
	return Tool{
		Name:        "server_exec",
		Description: "Execute one bounded non-interactive command as host root. Only visible to an unbound admin token, which is a full root credential.",
		InputSchema: objectSchema(map[string]any{
			"command": map[string]any{
				"type": "string", "description": "Command passed unchanged to /bin/sh -lc",
			},
			"timeoutSeconds": map[string]any{
				"type": "integer", "minimum": 0,
				"description": "Zero or omitted uses the server default",
			},
		}, []string{"command"}),
	}
}

type serverExecToolOutput struct {
	rootexec.Result
	RequestID string `json:"requestId"`
}

func (handler *Handler) executeServerCommand(
	ctx context.Context,
	arguments json.RawMessage,
	identity automation.Identity,
) (any, error) {
	var input struct {
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeoutSeconds"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	result, err := handler.serverExec.Execute(ctx, identity, automation.ServerExecInput{
		Command: input.Command, TimeoutSeconds: input.TimeoutSeconds,
	})
	if errors.Is(err, rootexec.ErrInvalidRequest) {
		return nil, fmt.Errorf("%w: %v", errInvalidArguments, err)
	}
	if err != nil {
		return nil, err
	}
	return serverExecToolOutput{
		Result: result.Execution, RequestID: result.RequestID,
	}, nil
}
