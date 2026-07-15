import { useState } from "react";
import type { FormEvent } from "react";

import type { Service } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

export interface ServiceConfigurationValues {
  healthPath?: string;
  imageReference: string;
  targetPort?: number;
}

export const ServiceConfiguration = ({
  busy,
  onSave,
  service,
}: {
  busy: boolean;
  onSave: (values: ServiceConfigurationValues) => Promise<boolean>;
  service: Service;
}) => {
  const [imageReference, setImageReference] = useState(service.imageReference);
  const [targetPort, setTargetPort] = useState(
    service.targetPort?.toString() ?? ""
  );
  const [healthPath, setHealthPath] = useState(service.healthPath ?? "");
  const [error, setError] = useState<string>();

  const save = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    try {
      const parsedPort = targetPort ? Number(targetPort) : undefined;
      if (
        parsedPort !== undefined &&
        (!Number.isInteger(parsedPort) || parsedPort < 1 || parsedPort > 65_535)
      ) {
        throw new Error("Target port must be between 1 and 65535");
      }
      const saved = await onSave({
        healthPath: healthPath.trim() || undefined,
        imageReference: imageReference.trim(),
        targetPort: parsedPort,
      });
      if (saved) {
        setError(undefined);
      }
    } catch (saveError) {
      setError(
        saveError instanceof Error
          ? saveError.message
          : "Invalid service configuration"
      );
    }
  };

  return (
    <form onSubmit={save}>
      <section className="grid border-b border-border lg:grid-cols-[14rem_minmax(18rem,1fr)]">
        <div className="px-5 py-4">
          <h3 className="text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
            Runtime
          </h3>
          <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
            Image, health probe, and internal service port.
          </p>
        </div>
        <div className="grid gap-3 border-t border-border px-5 py-4 lg:border-t-0 lg:border-l">
          <label
            className="grid gap-1.5 text-[9px] text-muted-foreground"
            htmlFor="service-image-reference"
          >
            Image reference
            <Input
              id="service-image-reference"
              onChange={(event) => setImageReference(event.target.value)}
              required
              value={imageReference}
            />
          </label>
          <div className="grid grid-cols-2 gap-3">
            <label
              className="grid gap-1.5 text-[9px] text-muted-foreground"
              htmlFor="service-target-port"
            >
              Target port
              <Input
                id="service-target-port"
                max={65_535}
                min={1}
                onChange={(event) => setTargetPort(event.target.value)}
                type="number"
                value={targetPort}
              />
            </label>
            <label
              className="grid gap-1.5 text-[9px] text-muted-foreground"
              htmlFor="service-health-path"
            >
              Health path
              <Input
                id="service-health-path"
                onChange={(event) => setHealthPath(event.target.value)}
                placeholder="/healthz"
                value={healthPath}
              />
            </label>
          </div>
        </div>
      </section>

      <div className="flex items-center justify-end gap-3 border-b border-border px-5 py-3">
        {error ? (
          <p className="mr-auto text-[10px] text-destructive">{error}</p>
        ) : null}
        <Button disabled={busy || imageReference.trim() === ""} type="submit">
          {busy ? "Saving…" : "Save configuration"}
        </Button>
      </div>
    </form>
  );
};
