import { Network, X } from "lucide-react";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";

import { fetchHostNetworkAddresses } from "@/api";
import type {
  HostNetworkAddress,
  NetworkGatewayInput,
  ProjectCanvas,
} from "@/api";
import { Button } from "@/components/ui/button";
import { NetworkGatewayForm } from "@/network-gateway-form";
import { emptyNetworkGatewayInput } from "@/network-gateway-form-model";

const validateInput = (input: NetworkGatewayInput) => {
  if (!input.name.trim()) {
    throw new Error("Name is required");
  }
  if (
    input.transport === "vpc" &&
    !(input.interfaceName && input.sourceAddress)
  ) {
    throw new Error("A VPC host network address is required");
  }
  if (input.listenPort < 1 || input.listenPort > 65_535) {
    throw new Error("Listener port must be between 1 and 65535");
  }
  if (
    input.mode === "import" &&
    (!input.remoteHost.trim() || input.remotePort < 1)
  ) {
    throw new Error("Imported endpoints require a remote host and port");
  }
  if (
    input.mode === "export" &&
    (!input.targetServiceId || input.targetPort < 1)
  ) {
    throw new Error("Exported endpoints require a target service and port");
  }
};

export const NetworkGatewayCreatePanel = ({
  onClose,
  onDrafted,
  projectID,
  resources,
}: {
  onClose: () => void;
  onDrafted: (input: NetworkGatewayInput) => void;
  projectID: string;
  resources: ProjectCanvas["resources"];
}) => {
  const [addresses, setAddresses] = useState<HostNetworkAddress[]>([]);
  const [input, setInput] = useState(emptyNetworkGatewayInput);
  const [error, setError] = useState<string>();
  const [meshConfigured, setMeshConfigured] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const loaded = await fetchHostNetworkAddresses(controller.signal);
        setAddresses(loaded);
        setInput((current) => {
          const [first] = loaded;
          if (!first || current.sourceAddress || current.transport !== "vpc") {
            return current;
          }
          return {
            ...current,
            interfaceName: first.interface,
            sourceAddress: first.address,
          };
        });
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(
            loadError instanceof Error
              ? loadError.message
              : "Unable to inspect host network"
          );
        }
      }
    };
    void load();
    return () => controller.abort();
  }, []);

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError(undefined);
    try {
      validateInput(input);
      if (input.transport === "mesh" && !meshConfigured) {
        throw new Error("Connect Cloudflare Mesh before adding this gateway");
      }
      onDrafted({
        ...input,
        name: input.name.trim(),
        remoteHost: input.remoteHost.trim(),
      });
    } catch (submitError) {
      setError(
        submitError instanceof Error
          ? submitError.message
          : "Unable to create network gateway draft"
      );
    }
  };

  return (
    <aside className="absolute inset-y-0 right-0 z-20 w-full max-w-4xl overflow-y-auto border-l border-border bg-background shadow-lg">
      <div className="flex h-12 items-center border-b border-border px-4">
        <Network className="size-4 text-muted-foreground" />
        <h2 className="ml-2 text-xs font-medium">New network gateway</h2>
        <Button
          aria-label="Close network gateway form"
          className="ml-auto"
          onClick={onClose}
          size="icon"
          variant="ghost"
        >
          <X />
        </Button>
      </div>
      <form className="grid gap-4 p-4" onSubmit={submit}>
        <NetworkGatewayForm
          addresses={addresses}
          input={input}
          onChange={setInput}
          onMeshConfiguredChange={setMeshConfigured}
          projectID={projectID}
          resources={resources}
        />
        {error ? (
          <p aria-live="polite" className="text-[10px] text-destructive">
            {error}
          </p>
        ) : null}
        <div className="flex justify-end gap-2 border-t border-border pt-4">
          <Button onClick={onClose} type="button" variant="ghost">
            Cancel
          </Button>
          <Button type="submit">Add gateway draft</Button>
        </div>
      </form>
    </aside>
  );
};
