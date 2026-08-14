package mcp

import "testing"

func TestServiceMutationToolsExposePortForwardConfiguration(t *testing.T) {
	for _, toolName := range []string{"create_service", "update_service"} {
		var tool *Tool
		tools := adminTools()
		for index := range tools {
			if tools[index].Name == toolName {
				tool = &tools[index]
				break
			}
		}
		if tool == nil {
			t.Fatalf("%s tool is missing", toolName)
		}

		properties := tool.InputSchema["properties"].(map[string]any)
		configuration := properties["configuration"].(map[string]any)
		configurationProperties := configuration["properties"].(map[string]any)
		portForward, ok := configurationProperties["portForward"].(map[string]any)
		if !ok {
			t.Fatalf("%s configuration.portForward schema is missing", toolName)
		}
		portForwardProperties := portForward["properties"].(map[string]any)
		if _, ok := portForwardProperties["repository"]; !ok {
			t.Fatalf("%s configuration.portForward.repository schema is missing", toolName)
		}
		if _, ok := portForwardProperties["workflows"]; !ok {
			t.Fatalf("%s configuration.portForward.workflows schema is missing", toolName)
		}
	}
}
