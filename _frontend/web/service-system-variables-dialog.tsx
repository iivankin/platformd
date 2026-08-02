import { Dialog } from "@base-ui/react/dialog";
import { Braces, Check, Minus, X } from "lucide-react";

import { Button } from "@/components/ui/button";

interface SystemVariableDefinition {
  build: "arg" | "env" | false;
  condition: string;
  name: string;
  runtime: boolean;
  value: string;
}

const systemVariables: SystemVariableDefinition[] = [
  {
    build: "env",
    condition: "GitHub builds; can be overridden",
    name: "CI",
    runtime: false,
    value: "1",
  },
  {
    build: "env",
    condition: "Can be overridden in the matching variables section",
    name: "NODE_ENV",
    runtime: true,
    value: "production",
  },
  {
    build: "env",
    condition: "Always",
    name: "PLATFORMD_ENVIRONMENT",
    runtime: true,
    value: "production or preview",
  },
  {
    build: "env",
    condition: "Always",
    name: "PLATFORMD_PROJECT_ID",
    runtime: true,
    value: "Current project ID",
  },
  {
    build: "env",
    condition: "Always",
    name: "PLATFORMD_PROJECT_NAME",
    runtime: true,
    value: "Current project name",
  },
  {
    build: "env",
    condition: "Always",
    name: "PLATFORMD_SERVICE_ID",
    runtime: true,
    value: "Current service ID",
  },
  {
    build: "env",
    condition: "Always",
    name: "PLATFORMD_SERVICE_NAME",
    runtime: true,
    value: "Current service name",
  },
  {
    build: "env",
    condition: "Always",
    name: "PLATFORMD_PRIVATE_DOMAIN",
    runtime: true,
    value: "Internal service hostname",
  },
  {
    build: "env",
    condition: "PR previews only",
    name: "PLATFORMD_PREVIEW",
    runtime: true,
    value: "true",
  },
  {
    build: "arg",
    condition: "Declare ARG; every GitHub deployment",
    name: "PLATFORMD_DEPLOYMENT_ID",
    runtime: true,
    value: "Current deployment ID",
  },
  {
    build: "arg",
    condition: "Declare ARG; every GitHub deployment",
    name: "PLATFORMD_PUBLIC_URLS",
    runtime: true,
    value: "Public URLs; preview URL for PR previews",
  },
  {
    build: "arg",
    condition: "Declare ARG; GitHub services",
    name: "PLATFORMD_GIT_REPOSITORY",
    runtime: true,
    value: "owner/repository",
  },
  {
    build: "arg",
    condition: "Declare ARG; GitHub deployments",
    name: "PLATFORMD_GIT_COMMIT_SHA",
    runtime: true,
    value: "Resolved commit SHA",
  },
  {
    build: "arg",
    condition: "Declare ARG; GitHub deployments",
    name: "PLATFORMD_GIT_COMMIT_MESSAGE",
    runtime: true,
    value: "Resolved commit message",
  },
  {
    build: "arg",
    condition: "Declare ARG; PR previews only",
    name: "PLATFORMD_PREVIEW_URL",
    runtime: true,
    value: "Public preview URL",
  },
  {
    build: "arg",
    condition: "Declare ARG; PR previews only",
    name: "PLATFORMD_GIT_PULL_REQUEST_NUMBER",
    runtime: true,
    value: "Pull request number",
  },
];

const ScopeCell = ({ enabled }: { enabled: boolean }) => (
  <span className="inline-flex w-full justify-center">
    {enabled ? (
      <Check className="size-3.5 text-emerald-500" />
    ) : (
      <Minus className="size-3.5 text-muted-foreground/50" />
    )}
  </span>
);

const BuildCell = ({ input }: { input: SystemVariableDefinition["build"] }) => (
  <span className="inline-flex w-full justify-center">
    {input ? (
      <code className="border border-border bg-muted/30 px-1.5 py-0.5 text-[8px] tracking-[0.08em] uppercase">
        {input}
      </code>
    ) : (
      <Minus className="size-3.5 text-muted-foreground/50" />
    )}
  </span>
);

export const ServiceSystemVariablesDialog = () => (
  <Dialog.Root>
    <Dialog.Trigger
      render={
        <Button size="sm" variant="outline">
          <Braces /> Automatic variables
        </Button>
      }
    />
    <Dialog.Portal>
      <Dialog.Backdrop className="fixed inset-0 z-50 bg-black/55 backdrop-blur-[1px] data-open:animate-in data-open:fade-in data-closed:animate-out data-closed:fade-out" />
      <Dialog.Viewport className="fixed inset-0 z-50 grid place-items-center overflow-y-auto p-4">
        <Dialog.Popup className="flex max-h-[calc(100dvh-2rem)] w-full max-w-5xl flex-col border border-border bg-background text-foreground shadow-2xl data-open:animate-in data-open:zoom-in-95 data-open:fade-in data-closed:animate-out data-closed:zoom-out-95 data-closed:fade-out">
          <header className="flex items-start justify-between gap-5 border-b border-border px-5 py-4">
            <div>
              <Dialog.Title className="text-sm font-medium">
                Automatic environment variables
              </Dialog.Title>
              <Dialog.Description className="mt-1.5 max-w-3xl text-[10px] leading-4 text-muted-foreground">
                platformd injects these values without adding rows to your
                configuration. Build ENV values are automatic; build ARG values
                become available where the Dockerfile declares them.
              </Dialog.Description>
            </div>
            <Dialog.Close
              aria-label="Close"
              className="flex size-8 shrink-0 items-center justify-center text-muted-foreground outline-none hover:bg-muted hover:text-foreground focus-visible:ring-1 focus-visible:ring-ring"
            >
              <X className="size-4" />
            </Dialog.Close>
          </header>

          <div className="overflow-auto">
            <div className="min-w-[52rem]">
              <div className="grid grid-cols-[minmax(17rem,1.2fr)_4.5rem_4.5rem_minmax(13rem,1fr)_minmax(13rem,1fr)] border-b border-border bg-muted/20 px-5 py-2 text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                <span>Variable</span>
                <span className="text-center">Build input</span>
                <span className="text-center">Runtime</span>
                <span>Value</span>
                <span>When</span>
              </div>
              {systemVariables.map((variable) => (
                <div
                  className="grid min-h-11 grid-cols-[minmax(17rem,1.2fr)_4.5rem_4.5rem_minmax(13rem,1fr)_minmax(13rem,1fr)] items-center border-b border-border px-5 text-[10px] last:border-b-0 hover:bg-muted/15"
                  key={variable.name}
                >
                  <code className="text-[10px] font-medium text-foreground">
                    {variable.name}
                  </code>
                  <BuildCell input={variable.build} />
                  <ScopeCell enabled={variable.runtime} />
                  <span className="text-muted-foreground">
                    {variable.value}
                  </span>
                  <span className="text-muted-foreground">
                    {variable.condition}
                  </span>
                </div>
              ))}
            </div>
          </div>

          <footer className="border-t border-border bg-muted/15 px-5 py-3 text-[9px] leading-4 text-muted-foreground">
            <code>PLATFORMD_*</code> names are reserved and always win over
            configured values. <code>CI</code> and <code>NODE_ENV</code> are
            defaults: defining them explicitly in Build time variables or
            Service variables overrides the matching default. An ARG value
            affects the layer cache only after its matching{" "}
            <code>ARG NAME</code>
            instruction.
          </footer>
        </Dialog.Popup>
      </Dialog.Viewport>
    </Dialog.Portal>
  </Dialog.Root>
);
