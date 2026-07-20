import { Network, X } from "lucide-react";
import { useEffect, useState } from "react";
import { Link, Navigate } from "react-router";

import { fetchHostNetworkAddresses } from "@/api";
import type { HostNetworkAddress, ProjectCanvas } from "@/api";
import { PageStack } from "@/components/ui/page-stack";
import { NetworkGatewayForm } from "@/network-gateway-form";
import { NetworkGatewayVariables } from "@/network-gateway-variables";
import { PageTabs } from "@/page-tabs";
import type { PendingResourceCreation } from "@/pending-resource-creation";
import { resourcePath } from "@/project-resource-path";
import { ResourceDrawer } from "@/resource-drawer";
import { WorkspaceView } from "@/workspace-view";

type GatewayDraft = Extract<
  PendingResourceCreation,
  { kind: "network_gateway" }
>;

const draftEndpoint = (draft: GatewayDraft, hostname: string) => {
  if (draft.input.mode === "import") {
    return hostname;
  }
  if (draft.input.transport === "mesh") {
    return `Cloudflare Mesh :${draft.input.listenPort}`;
  }
  return `${draft.input.sourceAddress}:${draft.input.listenPort}`;
};

export const NetworkGatewayDraftPage = ({
  draft,
  onChange,
  projectID,
  projectName,
  resources,
  view,
}: {
  draft: GatewayDraft;
  onChange: (draft: GatewayDraft) => void;
  projectID: string;
  projectName: string;
  resources: ProjectCanvas["resources"];
  view: string;
}) => {
  const [addresses, setAddresses] = useState<HostNetworkAddress[]>([]);
  const closePath = `/projects/${encodeURIComponent(projectID)}`;
  const validView = view === "variables" || view === "settings";
  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        setAddresses(await fetchHostNetworkAddresses(controller.signal));
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setAddresses([]);
        }
      }
    };
    void load();
    return () => controller.abort();
  }, []);
  if (!validView) {
    return (
      <Navigate
        replace
        to={resourcePath(projectID, draft.id, "network_gateway", "variables")}
      />
    );
  }
  const hostname = `${draft.input.name}.${projectName}.internal`;
  const tabs = [
    {
      label: "Variables",
      path: resourcePath(projectID, draft.id, "network_gateway", "variables"),
    },
    {
      label: "Settings",
      path: resourcePath(projectID, draft.id, "network_gateway", "settings"),
    },
  ];
  return (
    <ResourceDrawer closePath={closePath} label={`${draft.input.name} draft`}>
      <section className="flex min-h-20 items-center gap-4 border-b border-border px-5 py-4">
        <Link
          aria-label="Close network gateway draft"
          className="grid size-8 shrink-0 place-items-center border border-border text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          to={closePath}
        >
          <X className="size-3.5" />
        </Link>
        <span className="grid size-9 shrink-0 place-items-center bg-muted/50">
          <Network className="size-4 text-muted-foreground" />
        </span>
        <div className="min-w-0">
          <p className="text-[8px] tracking-[0.14em] text-muted-foreground uppercase">
            {projectName} / Network gateway draft
          </p>
          <h2 className="mt-1 truncate text-sm font-medium">
            {draft.input.name}
          </h2>
          <p className="mt-1 truncate text-[9px] text-muted-foreground">
            {draftEndpoint(draft, hostname)}
          </p>
        </div>
        <div className="ml-auto flex items-center gap-2 text-[9px] text-sky-600 dark:text-sky-300">
          <span className="size-1.5 bg-sky-500" />
          Draft
        </div>
      </section>
      <PageTabs label={`${draft.input.name} draft pages`} tabs={tabs} />
      <div className="min-h-0 flex-1 overflow-auto">
        <WorkspaceView
          active={view}
          views={{
            settings: (
              <PageStack>
                <NetworkGatewayForm
                  addresses={addresses}
                  input={draft.input}
                  onChange={(input) => onChange({ ...draft, input })}
                  projectID={projectID}
                  resources={resources}
                />
              </PageStack>
            ),
            variables: (
              <NetworkGatewayVariables
                hostname={hostname}
                mode={draft.input.mode}
                port={draft.input.listenPort}
              />
            ),
          }}
        />
      </div>
    </ResourceDrawer>
  );
};
