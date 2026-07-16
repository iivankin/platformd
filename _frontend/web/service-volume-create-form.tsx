import { useEffect, useState } from "react";

import { createVolume, fetchVolumeOwnerSuggestion } from "@/api";
import type { Volume } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { FormField } from "@/form-field";

const maximumOwnerID = 4_294_967_294;

interface ServiceVolumeCreateFormProperties {
  onCancel: () => void;
  onCreated: (volume: Volume) => void;
  projectID: string;
  serviceID: string;
}

export const ServiceVolumeCreateForm = ({
  onCancel,
  onCreated,
  projectID,
  serviceID,
}: ServiceVolumeCreateFormProperties) => {
  const [name, setName] = useState("");
  const [ownerUID, setOwnerUID] = useState("0");
  const [ownerGID, setOwnerGID] = useState("0");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    const suggest = async () => {
      try {
        const result = await fetchVolumeOwnerSuggestion(
          projectID,
          serviceID,
          controller.signal
        );
        setOwnerUID(String(result.ownerUid));
        setOwnerGID(String(result.ownerGid));
      } catch (suggestionError) {
        if (
          !(
            suggestionError instanceof DOMException &&
            suggestionError.name === "AbortError"
          )
        ) {
          setOwnerUID("0");
          setOwnerGID("0");
        }
      }
    };
    void suggest();
    return () => controller.abort();
  }, [projectID, serviceID]);

  const create = async () => {
    const parsedUID = Number(ownerUID);
    const parsedGID = Number(ownerGID);
    if (
      !Number.isSafeInteger(parsedUID) ||
      parsedUID < 0 ||
      parsedUID > maximumOwnerID ||
      !Number.isSafeInteger(parsedGID) ||
      parsedGID < 0 ||
      parsedGID > maximumOwnerID
    ) {
      setError(`UID and GID must be integers from 0 to ${maximumOwnerID}.`);
      return;
    }
    setBusy(true);
    setError(undefined);
    try {
      onCreated(
        await createVolume(projectID, serviceID, {
          name: name.trim(),
          ownerGid: parsedGID,
          ownerUid: parsedUID,
        })
      );
    } catch (createError) {
      setError(
        createError instanceof Error
          ? createError.message
          : "Unable to create volume"
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="border-t border-border bg-muted/20 px-4 py-4">
      <FormField label="Volume name" name="new-volume-name">
        <Input
          id="new-volume-name"
          onChange={(event) => setName(event.target.value)}
          placeholder="data"
          value={name}
        />
      </FormField>
      <div className="mt-3 flex gap-2">
        <Button
          disabled={busy || !name.trim()}
          onClick={() => void create()}
          size="sm"
        >
          Create volume
        </Button>
        <Button disabled={busy} onClick={onCancel} size="sm" variant="ghost">
          Cancel
        </Button>
      </div>
      {error ? (
        <p aria-live="polite" className="mt-3 text-[10px] text-destructive">
          {error}
        </p>
      ) : null}
    </div>
  );
};
