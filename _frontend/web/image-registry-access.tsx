import { KeyRound, LockKeyhole, Plus } from "lucide-react";
import { useState } from "react";

import type { ImageCredential } from "@/api";
import { Button } from "@/components/ui/button";
import { ImageCredentialForm } from "@/image-credential-form";
import {
  imageRegistryHost,
  isEmbeddedRegistryReference,
  matchingImageCredentials,
} from "@/image-registry";

export const ImageRegistryAccess = ({
  credentials,
  embeddedRegistryHost,
  id,
  imageReference,
  onCredentialCreated,
  onCredentialSelect,
  projectID,
  selectedCredentialID,
}: {
  credentials: ImageCredential[];
  embeddedRegistryHost: string;
  id: string;
  imageReference: string;
  onCredentialCreated: (credential: ImageCredential) => void;
  onCredentialSelect: (credentialID: string) => void;
  projectID: string;
  selectedCredentialID: string;
}) => {
  const [credentialOpen, setCredentialOpen] = useState(false);
  const registryHost = imageRegistryHost(imageReference);
  const embedded = isEmbeddedRegistryReference(
    imageReference,
    embeddedRegistryHost
  );
  const matchingCredentials = matchingImageCredentials(
    credentials,
    imageReference
  );

  return (
    <div className="grid gap-1.5">
      <div className="flex flex-wrap items-baseline justify-between gap-2 text-[9px] text-muted-foreground">
        <label htmlFor={id}>Registry access</label>
        <span>
          Host:{" "}
          {registryHost ? <code>{registryHost}</code> : "invalid reference"}
        </span>
      </div>

      {embedded ? (
        <div className="flex h-8 items-center gap-2 border border-input px-2 text-[10px] text-foreground">
          <LockKeyhole className="size-3.5 text-emerald-600" />
          Built-in registry
          <span className="ml-auto text-muted-foreground">
            Authentication is automatic
          </span>
        </div>
      ) : (
        <>
          <div className="flex gap-2">
            <select
              className="h-8 min-w-0 flex-1 border border-input bg-background px-2 text-xs text-foreground outline-none focus:border-ring disabled:text-muted-foreground"
              disabled={!registryHost}
              id={id}
              onChange={(event) => onCredentialSelect(event.target.value)}
              value={selectedCredentialID}
            >
              <option value="">No credential · anonymous pull</option>
              {matchingCredentials.map((credential) => (
                <option key={credential.id} value={credential.id}>
                  {credential.name} · {credential.username}
                </option>
              ))}
            </select>
            <Button
              disabled={!registryHost}
              onClick={() => setCredentialOpen((current) => !current)}
              size="sm"
              type="button"
              variant="outline"
            >
              {credentialOpen ? <KeyRound /> : <Plus />}
              {credentialOpen ? "Close" : "New credential"}
            </Button>
          </div>
          <p className="text-[9px] leading-4 text-muted-foreground">
            {registryHost
              ? `Only credentials saved for ${registryHost} are available here.`
              : "Enter a valid image reference to configure registry access."}
          </p>
        </>
      )}

      {credentialOpen && registryHost && !embedded ? (
        <ImageCredentialForm
          key={registryHost}
          onCancel={() => setCredentialOpen(false)}
          onCreated={(credential) => {
            onCredentialCreated(credential);
            onCredentialSelect(credential.id);
            setCredentialOpen(false);
          }}
          projectID={projectID}
          registryHost={registryHost}
        />
      ) : null}
    </div>
  );
};
