import { SectionCard } from "@/components/ui/card";
import { PageStack } from "@/components/ui/page-stack";

const rows = (hostname: string, port: number) => [
  { name: "HOST", value: hostname },
  { name: "PORT", value: String(port) },
  { name: "ADDRESS", value: `${hostname}:${port}` },
];

export const NetworkGatewayVariables = ({
  hostname,
  mode,
  port,
}: {
  hostname: string;
  mode: "export" | "import";
  port: number;
}) => (
  <PageStack>
    <SectionCard>
      <header className="border-b border-border px-5 py-4">
        <h3 className="text-[10px] font-medium">Exported variables</h3>
        <p className="mt-1 text-[9px] leading-4 text-muted-foreground">
          {mode === "import"
            ? "Reference this imported endpoint from service variables."
            : "Export gateways publish a service outward and do not create another internal endpoint."}
        </p>
      </header>
      {mode === "import" ? (
        <>
          <div className="grid grid-cols-[13rem_minmax(0,1fr)] border-b border-border px-5 py-2 text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
            <span>Name</span>
            <span>Value</span>
          </div>
          {rows(hostname, port).map((row) => (
            <div
              className="grid min-h-11 grid-cols-[13rem_minmax(0,1fr)] items-center border-b border-border px-5 text-[10px]"
              key={row.name}
            >
              <code>{row.name}</code>
              <code className="truncate text-muted-foreground">
                {row.value}
              </code>
            </div>
          ))}
        </>
      ) : (
        <p className="px-5 py-5 text-[10px] text-muted-foreground">
          No exported variables.
        </p>
      )}
    </SectionCard>
  </PageStack>
);
