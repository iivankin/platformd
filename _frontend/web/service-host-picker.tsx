import { useEffect, useState } from "react";

import { fetchHosts } from "@/api";
import type { Host } from "@/api";
import { FieldSelect } from "@/field-select";

export const primaryHostValue = "__primary__";

export const hostSelectValue = (hostID?: string) =>
  hostID && hostID !== "" ? hostID : primaryHostValue;

export const hostIDFromSelectValue = (value: string) =>
  value === primaryHostValue ? "" : value;

export const ServiceHostPicker = ({
  disabled,
  id,
  onChange,
  value,
}: {
  disabled?: boolean;
  id?: string;
  onChange: (hostId: string) => void;
  value: string;
}) => {
  const [hosts, setHosts] = useState<Host[]>([]);
  const [error, setError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        setHosts(await fetchHosts(controller.signal));
        setError(undefined);
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(
            loadError instanceof Error
              ? loadError.message
              : "Unable to load child servers"
          );
        }
      }
    };
    void load();
    return () => controller.abort();
  }, []);

  const items = [
    { label: "Primary VPS", value: primaryHostValue },
    ...hosts.map((host) => ({
      label: host.connected
        ? `${host.name} · connected`
        : `${host.name} · offline`,
      value: host.id,
    })),
  ];
  if (value !== "" && !items.some((item) => item.value === value)) {
    items.push({ label: value, value });
  }

  return (
    <div className="grid gap-1.5">
      <FieldSelect
        aria-label="Service server"
        disabled={disabled}
        id={id}
        items={items}
        onValueChange={(next) => onChange(hostIDFromSelectValue(next))}
        value={hostSelectValue(value)}
      />
      {error ? <p className="text-[10px] text-destructive">{error}</p> : null}
    </div>
  );
};
