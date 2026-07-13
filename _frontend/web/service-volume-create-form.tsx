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
  const [ownerNote, setOwnerNote] = useState("Loading the active image user…");
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
        if (result.exactNumeric) {
          setOwnerNote(
            `Image declares numeric user ${result.ownerUid}:${result.ownerGid}. You can edit it before creation.`
          );
        } else if (result.imageUser) {
          setOwnerNote(
            `Image user “${result.imageUser}” is not an exact uid:gid pair. Defaulting to 0:0.`
          );
        } else {
          setOwnerNote(
            "The active image does not declare an exact numeric uid:gid pair. Defaulting to 0:0."
          );
        }
      } catch (suggestionError) {
        if (
          !(
            suggestionError instanceof DOMException &&
            suggestionError.name === "AbortError"
          )
        ) {
          setOwnerNote(
            "Image ownership could not be inspected. Defaulting to 0:0."
          );
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
      <div className="grid grid-cols-2 gap-3">
        <FormField label="Owner UID" name="new-volume-uid">
          <Input
            id="new-volume-uid"
            max={maximumOwnerID}
            min="0"
            onChange={(event) => setOwnerUID(event.target.value)}
            type="number"
            value={ownerUID}
          />
        </FormField>
        <FormField label="Owner GID" name="new-volume-gid">
          <Input
            id="new-volume-gid"
            max={maximumOwnerID}
            min="0"
            onChange={(event) => setOwnerGID(event.target.value)}
            type="number"
            value={ownerGID}
          />
        </FormField>
      </div>
      <p className="text-[9px] leading-4 text-muted-foreground">
        {ownerNote} Ownership is applied once to the empty directory; deploys
        never recursively chown existing data.
      </p>
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
