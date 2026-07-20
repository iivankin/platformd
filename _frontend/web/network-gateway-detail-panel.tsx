import { Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { useNavigate } from "react-router";

import {
  deleteNetworkGateway,
  fetchHostNetworkAddresses,
  fetchNetworkGateway,
  updateNetworkGateway,
} from "@/api";
import type {
  HostNetworkAddress,
  NetworkGateway,
  NetworkGatewayInput,
  ProjectCanvas,
} from "@/api";
import { Button } from "@/components/ui/button";
import { PageStack } from "@/components/ui/page-stack";
import { NetworkGatewayForm } from "@/network-gateway-form";
import { NetworkGatewayVariables } from "@/network-gateway-variables";
import { WorkspaceView } from "@/workspace-view";

export type NetworkGatewayWorkspaceView = "settings" | "variables";

const gatewayInput = (gateway: NetworkGateway): NetworkGatewayInput => ({
  interfaceName: gateway.interfaceName,
  listenPort: gateway.listenPort,
  mode: gateway.mode,
  name: gateway.name,
  protocol: gateway.protocol,
  remoteHost: gateway.remoteHost,
  remotePort: gateway.remotePort,
  sourceAddress: gateway.sourceAddress,
  targetPort: gateway.targetPort,
  targetServiceId: gateway.targetServiceId,
  transport: gateway.transport,
});

export const NetworkGatewayDetailPanel = ({
  onChanged,
  projectID,
  gatewayID,
  resources,
  view,
}: {
  onChanged: () => void;
  projectID: string;
  gatewayID: string;
  resources: ProjectCanvas["resources"];
  view: NetworkGatewayWorkspaceView;
}) => {
  const navigate = useNavigate();
  const [gateway, setGateway] = useState<NetworkGateway>();
  const [input, setInput] = useState<NetworkGatewayInput>();
  const [addresses, setAddresses] = useState<HostNetworkAddress[]>([]);
  const [error, setError] = useState<string>();
  const [saving, setSaving] = useState(false);
  const [confirmingDelete, setConfirmingDelete] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const [loaded, loadedAddresses] = await Promise.all([
          fetchNetworkGateway(projectID, gatewayID, controller.signal),
          fetchHostNetworkAddresses(controller.signal),
        ]);
        setGateway(loaded);
        setInput(gatewayInput(loaded));
        setAddresses(loadedAddresses);
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(
            loadError instanceof Error
              ? loadError.message
              : "Unable to load network gateway"
          );
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [gatewayID, projectID]);

  if (!(gateway && input)) {
    return (
      <div className="grid min-h-56 place-items-center px-6 text-[10px] text-muted-foreground">
        {error ?? "Loading network gateway…"}
      </div>
    );
  }
  const save = async () => {
    setSaving(true);
    setError(undefined);
    try {
      const updated = await updateNetworkGateway(projectID, gatewayID, input);
      setGateway(updated);
      setInput(gatewayInput(updated));
      onChanged();
    } catch (saveError) {
      setError(
        saveError instanceof Error
          ? saveError.message
          : "Unable to update network gateway"
      );
    } finally {
      setSaving(false);
    }
  };
  const remove = async () => {
    setSaving(true);
    try {
      await deleteNetworkGateway(projectID, gatewayID);
      onChanged();
      void navigate(`/projects/${encodeURIComponent(projectID)}`);
    } catch (deleteError) {
      setError(
        deleteError instanceof Error
          ? deleteError.message
          : "Unable to delete network gateway"
      );
      setSaving(false);
    }
  };
  return (
    <WorkspaceView
      active={view}
      views={{
        settings: (
          <PageStack>
            <NetworkGatewayForm
              addresses={addresses}
              input={input}
              onChange={setInput}
              projectID={projectID}
              resources={resources}
            />
            {error ? (
              <p className="text-[10px] text-destructive">{error}</p>
            ) : null}
            <div className="flex items-center justify-between border border-border px-4 py-3">
              <Button
                disabled={saving}
                onClick={() => {
                  if (confirmingDelete) {
                    void remove();
                    return;
                  }
                  setConfirmingDelete(true);
                }}
                variant="destructive"
              >
                <Trash2 />
                {confirmingDelete
                  ? `Confirm delete ${gateway.name}`
                  : "Delete gateway"}
              </Button>
              <Button disabled={saving} onClick={() => void save()}>
                {saving ? "Saving…" : "Save settings"}
              </Button>
            </div>
          </PageStack>
        ),
        variables: (
          <NetworkGatewayVariables
            hostname={gateway.internalHostname ?? ""}
            mode={gateway.mode}
            port={gateway.listenPort}
          />
        ),
      }}
    />
  );
};
