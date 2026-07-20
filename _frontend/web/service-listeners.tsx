import { Network, Plus, Trash2 } from "lucide-react";
import { useState } from "react";

import type { ContainerPort } from "@/api";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { ContainerPortCombobox } from "@/container-port-combobox";
import type { ServiceListenerDraft } from "@/service-settings-model";
import { serviceListenerDraftKey } from "@/service-settings-model";
import type { ContainerPortDetectionStatus } from "@/use-container-ports";

interface ServiceListenersProperties {
  containerPorts: ContainerPort[];
  containerPortsStatus?: ContainerPortDetectionStatus;
  disabled?: boolean;
  listeners: ServiceListenerDraft[];
  onChanged: (listeners: ServiceListenerDraft[]) => void;
}

const validPort = (port: number) =>
  Number.isInteger(port) && port >= 1 && port <= 65_535;
export const ServiceListeners = ({
  containerPorts,
  containerPortsStatus = "ready",
  disabled = false,
  listeners,
  onChanged,
}: ServiceListenersProperties) => {
  const [protocol, setProtocol] =
    useState<ServiceListenerDraft["protocol"]>("tcp");
  const [publicPort, setPublicPort] = useState(0);
  const [targetPort, setTargetPort] = useState(0);
  const [error, setError] = useState<string>();

  const commit = (listener: ServiceListenerDraft) => {
    const key = serviceListenerDraftKey(listener);
    onChanged(
      [
        ...listeners.filter(
          (current) => serviceListenerDraftKey(current) !== key
        ),
        listener,
      ].toSorted(
        (left, right) =>
          left.publicPort - right.publicPort ||
          left.protocol.localeCompare(right.protocol)
      )
    );
  };

  const add = (input: {
    protocol: ServiceListenerDraft["protocol"];
    publicPort: number;
    targetPort: number;
  }) => {
    if (
      disabled ||
      !validPort(input.publicPort) ||
      !validPort(input.targetPort)
    ) {
      return;
    }
    setError(undefined);
    commit(input);
    setPublicPort(0);
    setTargetPort(0);
  };

  const remove = (listener: ServiceListenerDraft) =>
    onChanged(
      listeners.filter(
        (current) =>
          serviceListenerDraftKey(current) !== serviceListenerDraftKey(listener)
      )
    );

  return (
    <SectionCard>
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
            const key = serviceListenerDraftKey(listener);
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
                <ContainerPortCombobox
                  ariaLabel={`Container port for ${listener.protocol} ${listener.publicPort}`}
                  className="h-7 px-2 text-[9px]"
                  disabled={disabled}
                  onChange={(nextPort) =>
                    commit({
                      ...listener,
                      targetPort: nextPort,
                    })
                  }
                  ports={containerPorts}
                  protocol={listener.protocol}
                  status={containerPortsStatus}
                  value={listener.targetPort}
                />
                <Button
                  aria-label={`Remove ${listener.protocol} ${listener.publicPort}`}
                  disabled={disabled}
                  onClick={() => remove(listener)}
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
        <Select
          disabled={disabled}
          items={{ tcp: "TCP", udp: "UDP" }}
          onValueChange={(value) =>
            setProtocol(String(value) as ServiceListenerDraft["protocol"])
          }
          value={protocol}
        >
          <SelectTrigger
            aria-label="Listener protocol"
            className="h-8 w-full text-[10px]"
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent align="start">
            <SelectItem value="tcp">TCP</SelectItem>
            <SelectItem value="udp">UDP</SelectItem>
          </SelectContent>
        </Select>
        <Input
          aria-label="Public VPS port"
          disabled={disabled}
          max={65_535}
          min={1}
          onChange={(event) => setPublicPort(Number(event.target.value))}
          placeholder="Public port"
          type="number"
          value={publicPort || ""}
        />
        <ContainerPortCombobox
          ariaLabel="Listener container port"
          disabled={disabled}
          onChange={setTargetPort}
          placeholder="Container port"
          ports={containerPorts}
          protocol={protocol}
          status={containerPortsStatus}
          value={targetPort}
        />
        <Button
          disabled={
            disabled || !validPort(publicPort) || !validPort(targetPort)
          }
          onClick={() => add({ protocol, publicPort, targetPort })}
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
    </SectionCard>
  );
};
