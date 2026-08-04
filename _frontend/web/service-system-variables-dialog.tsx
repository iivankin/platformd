import { Dialog } from "@base-ui/react/dialog";
import { Braces, X } from "lucide-react";

import { Button } from "@/components/ui/button";

interface SystemVariableDefinition {
  condition: string;
  name: string;
  value: string;
}

const systemVariables: SystemVariableDefinition[] = [
  {
    condition: "Can be overridden by a service variable",
    name: "NODE_ENV",
    value: "production",
  },
  {
    condition: "Always",
    name: "PLATFORMD_ENVIRONMENT",
    value: "production or preview",
  },
  {
    condition: "Always",
    name: "PLATFORMD_PROJECT_ID",
    value: "Current project ID",
  },
  {
    condition: "Always",
    name: "PLATFORMD_PROJECT_NAME",
    value: "Current project name",
  },
  {
    condition: "Always",
    name: "PLATFORMD_SERVICE_ID",
    value: "Current service ID",
  },
  {
    condition: "Always",
    name: "PLATFORMD_SERVICE_NAME",
    value: "Current service name",
  },
  {
    condition: "Always",
    name: "PLATFORMD_PRIVATE_DOMAIN",
    value: "Internal service hostname",
  },
  {
    condition: "Image previews only",
    name: "PLATFORMD_PREVIEW",
    value: "true",
  },
  {
    condition: "Always",
    name: "PLATFORMD_DEPLOYMENT_ID",
    value: "Current deployment ID",
  },
  {
    condition: "Always",
    name: "PLATFORMD_PUBLIC_URLS",
    value: "Production URLs or the preview URL",
  },
  {
    condition: "Image previews only",
    name: "PLATFORMD_PREVIEW_URL",
    value: "Public preview URL",
  },
];

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
                service configuration.
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
            <div className="min-w-[42rem]">
              <div className="grid grid-cols-[minmax(17rem,1.2fr)_minmax(13rem,1fr)_minmax(13rem,1fr)] border-b border-border bg-muted/20 px-5 py-2 text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                <span>Variable</span>
                <span>Value</span>
                <span>When</span>
              </div>
              {systemVariables.map((variable) => (
                <div
                  className="grid min-h-11 grid-cols-[minmax(17rem,1.2fr)_minmax(13rem,1fr)_minmax(13rem,1fr)] items-center border-b border-border px-5 text-[10px] last:border-b-0 hover:bg-muted/15"
                  key={variable.name}
                >
                  <code className="text-[10px] font-medium text-foreground">
                    {variable.name}
                  </code>
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
            configured values. <code>NODE_ENV</code> is a default: defining it
            as a service variable overrides that default.
          </footer>
        </Dialog.Popup>
      </Dialog.Viewport>
    </Dialog.Portal>
  </Dialog.Root>
);
