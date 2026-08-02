import { CheckCircle2, LoaderCircle } from "lucide-react";
import { useEffect, useState } from "react";

import { fetchServiceDomainDNSStatus } from "@/api";
import type { ServiceDomainDNSStatus as DNSStatus } from "@/api";

const pollingIntervalMilliseconds = 2000;

export const ServiceDomainDNSStatus = ({
  hostname,
  projectID,
  serviceID,
}: {
  hostname: string;
  projectID: string;
  serviceID: string;
}) => {
  const [status, setStatus] = useState<DNSStatus | "checking" | "retrying">(
    "checking"
  );

  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;

    const check = async () => {
      try {
        const next = await fetchServiceDomainDNSStatus(
          projectID,
          serviceID,
          hostname,
          controller.signal
        );
        if (controller.signal.aborted) {
          return;
        }
        setStatus(next);
        if (next === "pending") {
          timer = setTimeout(() => void check(), pollingIntervalMilliseconds);
        }
      } catch (error) {
        if (
          controller.signal.aborted ||
          (error instanceof DOMException && error.name === "AbortError")
        ) {
          return;
        }
        setStatus("retrying");
        timer = setTimeout(() => void check(), pollingIntervalMilliseconds);
      }
    };

    void check();
    return () => {
      controller.abort();
      if (timer !== undefined) {
        clearTimeout(timer);
      }
    };
  }, [hostname, projectID, serviceID]);

  if (status === "unmanaged") {
    return null;
  }
  if (status === "ready") {
    return (
      <output
        aria-label={`DNS for ${hostname} is ready`}
        className="grid size-4 place-items-center text-emerald-600 dark:text-emerald-400"
        title="DNS is updated"
      >
        <CheckCircle2 className="size-3" />
      </output>
    );
  }

  const tooltip =
    status === "retrying"
      ? "Unable to verify DNS right now. Retrying…"
      : "Waiting for DNS propagation…";
  return (
    <output
      aria-label={tooltip}
      className="grid size-4 place-items-center text-muted-foreground"
      title={tooltip}
    >
      <LoaderCircle className="size-3 animate-spin" />
    </output>
  );
};
