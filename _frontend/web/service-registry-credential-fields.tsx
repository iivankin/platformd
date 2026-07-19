import { LockKeyhole } from "lucide-react";

import { Input } from "@/components/ui/input";
import { imageRegistryHost } from "@/image-registry";

export const ServiceRegistryCredentialFields = ({
  imageReference,
  onChange,
  password,
  username,
}: {
  imageReference: string;
  onChange: (credential: { password: string; username: string }) => void;
  password: string;
  username: string;
}) => {
  const host = imageRegistryHost(imageReference);
  const hasImageReference = imageReference.trim() !== "";
  return (
    <div className="grid gap-3 border-t border-border pt-3 md:grid-cols-2">
      <div className="flex items-center gap-2 text-[9px] text-muted-foreground md:col-span-2">
        <LockKeyhole className="size-3" />
        <span>Credentials belong only to this service.</span>
        {hasImageReference ? (
          <span
            className={`ml-auto font-mono ${host ? "" : "text-destructive"}`}
          >
            {host || "Invalid image reference"}
          </span>
        ) : null}
      </div>
      <label
        className="grid gap-1.5 text-[9px] text-muted-foreground"
        htmlFor="service-registry-username"
      >
        Username
        <Input
          autoCapitalize="none"
          autoComplete="off"
          id="service-registry-username"
          onChange={(event) =>
            onChange({ password, username: event.target.value })
          }
          spellCheck={false}
          value={username}
        />
      </label>
      <label
        className="grid gap-1.5 text-[9px] text-muted-foreground"
        htmlFor="service-registry-password"
      >
        Password or token
        <Input
          autoComplete="off"
          id="service-registry-password"
          onChange={(event) =>
            onChange({ password: event.target.value, username })
          }
          value={password}
        />
      </label>
    </div>
  );
};
