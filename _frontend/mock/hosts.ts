import type { HostJoinToken } from "../web/api";
import { json, mockError, noContent, readObject, stringField } from "./http";
import type { MockState } from "./state";
import { mockNow, nextMockID } from "./state";

export const handleHostsAPI = async (
  request: Request,
  state: MockState,
  segments: string[]
): Promise<Response | undefined> => {
  const [root, second, third] = segments;
  if (root !== "hosts") {
    return undefined;
  }
  if (second === "join-tokens") {
    if (request.method === "GET" && !third) {
      return json({ tokens: state.hostJoinTokens });
    }
    if (request.method === "DELETE" && third) {
      state.hostJoinTokens = state.hostJoinTokens.filter(
        (token) => token.id !== third
      );
      return noContent();
    }
    if (request.method === "POST" && !third) {
      const input = await readObject(request);
      const name = stringField(input, "name", "edge");
      const token = `pjn_mock_${nextMockID(state, "join")}`;
      const created: HostJoinToken = {
        command: `curl -fsSL https://raw.githubusercontent.com/iivankin/platformd/main/install.sh | sudo sh -s -- worker\nsudo platformd join --url https://${state.settings.adminHostname} --token ${token}`,
        createdAt: mockNow(),
        expiresAt: mockNow() + 24 * 60 * 60_000,
        id: nextMockID(state, "host-token"),
        name,
        token,
      };
      state.hostJoinTokens = [
        {
          createdAt: created.createdAt,
          expiresAt: created.expiresAt,
          id: created.id,
          name: created.name,
        },
        ...state.hostJoinTokens,
      ];
      return json(created, 201);
    }
    return undefined;
  }
  if (request.method === "GET" && !second) {
    return json({ hosts: state.hosts });
  }
  if (request.method === "DELETE" && second && !third) {
    const assigned = Object.values(state.services).some(
      (service) => service.hostId === second
    );
    if (assigned) {
      return mockError(
        "host_has_services",
        "host still has assigned services",
        409
      );
    }
    state.hosts = state.hosts.filter((host) => host.id !== second);
    return noContent();
  }
  return undefined;
};
