import { KeyRound, ShieldAlert } from "lucide-react";
import { useState } from "react";
import type { FormEvent } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

import { api } from "./api";
import { FormLabel } from "./common-ui";
import { FormFooter, Modal, SecretReveal } from "./dialog-frame";
import { errorMessage } from "./format";
import type { ApiToken, App } from "./types";

const allApps = "__all-apps__";

export const CreateTokenDialog = ({
  apps,
  defaultAppId,
  onCreated,
}: {
  apps: App[];
  defaultAppId: string;
  onCreated: () => Promise<void>;
}) => {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [role, setRole] = useState<ApiToken["role"]>("read");
  const [appId, setAppId] = useState(defaultAppId);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [secret, setSecret] = useState("");

  const reset = () => {
    setName("");
    setRole("read");
    setAppId(defaultAppId);
    setError("");
    setSecret("");
  };
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setPending(true);
    setError("");
    try {
      const created = await api.createToken(name.trim(), role, appId);
      setSecret(created.token);
      await onCreated();
    } catch (createError) {
      setError(errorMessage(createError, "Unable to create API token"));
    } finally {
      setPending(false);
    }
  };

  return (
    <>
      <Button onClick={() => setOpen(true)} size="sm">
        <KeyRound /> Create API token
      </Button>
      <Modal
        description="Scoped bearer credentials authorize the public REST API and MCP server."
        onOpenChange={(nextOpen) => {
          setOpen(nextOpen);
          if (!nextOpen) {
            reset();
          }
        }}
        open={open}
        title="Create API token"
      >
        {secret ? (
          <SecretReveal values={[{ label: "Bearer token", value: secret }]} />
        ) : (
          <form onSubmit={submit}>
            <div className="grid gap-4 px-5 py-5 sm:grid-cols-2">
              <div className="sm:col-span-2">
                <FormLabel htmlFor="token-name">Token name</FormLabel>
                <Input
                  autoFocus
                  id="token-name"
                  maxLength={80}
                  onChange={(event) => setName(event.target.value)}
                  placeholder="release-bot"
                  required
                  value={name}
                />
              </div>
              <div>
                <FormLabel htmlFor="token-role">Role</FormLabel>
                <Select
                  items={{ admin: "Admin — mutations", read: "Read only" }}
                  onValueChange={(value) =>
                    setRole(String(value) as ApiToken["role"])
                  }
                  value={role}
                >
                  <SelectTrigger className="w-full" id="token-role">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent align="start">
                    <SelectItem value="read">Read only</SelectItem>
                    <SelectItem value="admin">Admin — mutations</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div>
                <FormLabel htmlFor="token-scope">Application scope</FormLabel>
                <Select
                  items={[
                    { label: "All applications", value: allApps },
                    ...apps.map((app) => ({ label: app.name, value: app.id })),
                  ]}
                  onValueChange={(value) =>
                    setAppId(value === allApps ? "" : String(value))
                  }
                  value={appId || allApps}
                >
                  <SelectTrigger className="w-full" id="token-scope">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent align="start">
                    <SelectItem value={allApps}>All applications</SelectItem>
                    {apps.map((app) => (
                      <SelectItem key={app.id} value={app.id}>
                        {app.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </div>
            {role === "admin" && !appId ? (
              <div className="flex items-center gap-2 border-t border-amber-500/30 bg-amber-500/5 px-5 py-2.5 text-[10px] text-amber-700">
                <ShieldAlert className="size-3.5" />
                This token can mutate every application in the tracker.
              </div>
            ) : null}
            <FormFooter error={error} label="Create token" pending={pending} />
          </form>
        )}
      </Modal>
    </>
  );
};
