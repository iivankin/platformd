import { Network, Plus, Trash2 } from "lucide-react";
import { useState } from "react";

import { APIError, attachServiceListener, detachServiceListener } from "@/api";
import type { ServiceListener } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

interface ServiceListenersProperties {
  listeners: ServiceListener[];
  onChanged: (listeners: ServiceListener[]) => void;
  onPortDraftChange: (key: string, port: number) => void;
  portDrafts: Record<string, number>;
  projectID: string;
  serviceID: string;
}

const validPort = (port: number) =>
  Number.isInteger(port) && port >= 1 && port <= 65_535;
export const serviceListenerKey = (
  listener: Pick<ServiceListener, "protocol" | "publicPort">
) => `${listener.protocol}:${listener.publicPort}`;

export const ServiceListeners = ({
  listeners,
  onChanged,
  onPortDraftChange,
  portDrafts,
  projectID,
  serviceID,
}: ServiceListenersProperties) => {
  const [protocol, setProtocol] = useState<ServiceListener["protocol"]>("tcp");
  const [publicPort, setPublicPort] = useState(0);
  const [targetPort, setTargetPort] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  const commit = (listener: ServiceListener) => {
    const key = serviceListenerKey(listener);
    onChanged(
      [
        ...listeners.filter((current) => serviceListenerKey(current) !== key),
        listener,
      ].toSorted(
        (left, right) =>
          left.publicPort - right.publicPort ||
          left.protocol.localeCompare(right.protocol)
      )
    );
    onPortDraftChange(key, listener.targetPort);
  };

  const attach = async (input: {
    protocol: ServiceListener["protocol"];
    publicPort: number;
    targetPort: number;
  }) => {
    if (busy || !validPort(input.publicPort) || !validPort(input.targetPort)) {
      return;
    }
    setBusy(true);
    setError(undefined);
    try {
      commit(await attachServiceListener(projectID, serviceID, input));
      setPublicPort(0);
      setTargetPort(0);
    } catch (attachError) {
      if (
        attachError instanceof APIError &&
        attachError.code === "listener_conflict" &&
        attachError.listener
      ) {
        const owner = attachError.listener;
        setError(
          `Public ${owner.protocol.toUpperCase()} port ${owner.publicPort} is used by ${owner.serviceName ?? "another service"}${owner.projectName ? ` in ${owner.projectName}` : ""}.`
        );
      } else {
        setError(
          attachError instanceof Error
            ? attachError.message
            : "Unable to attach listener"
        );
      }
    } finally {
      setBusy(false);
    }
  };

  const remove = async (listener: ServiceListener) => {
    if (busy) {
      return;
    }
    setBusy(true);
    setError(undefined);
    try {
      await detachServiceListener(
        projectID,
        serviceID,
        listener.protocol,
        listener.publicPort
      );
      onChanged(
        listeners.filter(
          (current) =>
            serviceListenerKey(current) !== serviceListenerKey(listener)
        )
      );
    } catch (removeError) {
      setError(
        removeError instanceof Error
          ? removeError.message
          : "Unable to remove listener"
      );
    } finally {
      setBusy(false);
    }
  };

  const submit = () => {
    void attach({ protocol, publicPort, targetPort });
  };

  return (
    <section className="border-b border-border">
      <header className="flex min-h-14 items-center justify-between gap-4 bg-muted/25 px-5 py-3">
        <div>
          <h3 className="text-[10px] font-medium">TCP / UDP listeners</h3>
          <p className="mt-1 text-[9px] text-muted-foreground">
            Bind a VPS port and forward it directly to a container port.
          </p>
        </div>
        <Network className="size-4 shrink-0 text-muted-foreground" />
      </header>

      {listeners.length ? (
        <div className="border-t border-border">
          {listeners.map((listener) => {
            const key = serviceListenerKey(listener);
            const draft = portDrafts[key] ?? listener.targetPort;
            return (
              <div
                className="grid min-h-12 grid-cols-[4rem_minmax(0,1fr)_8rem_2.5rem] items-center border-b border-border px-5 last:border-b-0"
                key={key}
              >
                <span className="text-[9px] font-medium tracking-[0.1em] uppercase">
                  {listener.protocol}
                </span>
                <span className="text-[10px]">
                  VPS :{listener.publicPort} → container
                </span>
                <Input
                  aria-label={`Container port for ${listener.protocol} ${listener.publicPort}`}
                  className="h-7 px-2 text-[9px]"
                  disabled={busy}
                  max={65_535}
                  min={1}
                  onChange={(event) =>
                    onPortDraftChange(key, Number(event.target.value))
                  }
                  type="number"
                  value={draft}
                />
                <Button
                  aria-label={`Remove ${listener.protocol} ${listener.publicPort}`}
                  disabled={busy}
                  onClick={() => void remove(listener)}
                  size="icon"
                  variant="ghost"
                >
                  <Trash2 />
                </Button>
              </div>
            );
          })}
        </div>
      ) : (
        <p className="border-y border-dashed border-border px-5 py-4 text-[10px] text-muted-foreground">
          No direct public listener attached.
        </p>
      )}

      <div className="grid grid-cols-[6rem_8rem_8rem_auto] gap-2 bg-muted/10 px-5 py-3">
        <select
          aria-label="Listener protocol"
          className="h-8 border border-input bg-transparent px-2 text-[10px] text-foreground outline-none focus:border-ring"
          disabled={busy}
          onChange={(event) =>
            setProtocol(event.target.value as ServiceListener["protocol"])
          }
          value={protocol}
        >
          <option value="tcp">TCP</option>
          <option value="udp">UDP</option>
        </select>
        <Input
          aria-label="Public VPS port"
          disabled={busy}
          max={65_535}
          min={1}
          onChange={(event) => setPublicPort(Number(event.target.value))}
          placeholder="Public port"
          type="number"
          value={publicPort || ""}
        />
        <Input
          aria-label="Listener container port"
          disabled={busy}
          max={65_535}
          min={1}
          onChange={(event) => setTargetPort(Number(event.target.value))}
          placeholder="Container port"
          type="number"
          value={targetPort || ""}
        />
        <Button
          disabled={busy || !validPort(publicPort) || !validPort(targetPort)}
          onClick={submit}
          size="sm"
          type="button"
        >
          <Plus /> Add listener
        </Button>
      </div>
      {error ? (
        <p
          aria-live="polite"
          className="border-t border-destructive/30 bg-destructive/5 px-5 py-3 text-[10px] text-destructive"
        >
          {error}
        </p>
      ) : null}
    </section>
  );
};
