import { Dialog } from "@base-ui/react/dialog";
import { BookOpen, Check, Clipboard, X } from "lucide-react";
import { useState } from "react";
import type { ReactNode } from "react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

type YamlTokenKind =
  | "comment"
  | "expression"
  | "key"
  | "plain"
  | "punctuation"
  | "string";

interface YamlToken {
  kind: YamlTokenKind;
  text: string;
}

const yamlTokenClass: Record<YamlTokenKind, string> = {
  comment: "text-muted-foreground/80",
  expression: "text-sky-600 dark:text-sky-300",
  key: "text-emerald-700 dark:text-emerald-300",
  plain: "text-foreground",
  punctuation: "text-muted-foreground/70",
  string: "text-amber-700 dark:text-amber-300",
};

export const tokenizeYamlLine = (line: string): YamlToken[] => {
  const tokens: YamlToken[] = [];
  let index = 0;

  const pushLeading = () => {
    let end = index;
    while (end < line.length && (line[end] === " " || line[end] === "-")) {
      end += 1;
    }
    if (end > index) {
      tokens.push({ kind: "punctuation", text: line.slice(index, end) });
      index = end;
    }
  };

  const pushValue = (value: string) => {
    let cursor = 0;
    while (cursor < value.length) {
      if (value[cursor] === "#") {
        tokens.push({ kind: "comment", text: value.slice(cursor) });
        return;
      }

      if (value.startsWith("${{", cursor)) {
        const end = value.indexOf("}}", cursor);
        if (end !== -1) {
          tokens.push({
            kind: "expression",
            text: value.slice(cursor, end + 2),
          });
          cursor = end + 2;
          continue;
        }
      }

      if (value[cursor] === '"' || value[cursor] === "'") {
        const quote = value[cursor];
        let end = cursor + 1;
        while (end < value.length && value[end] !== quote) {
          end += 1;
        }
        if (end < value.length) {
          end += 1;
        }
        tokens.push({ kind: "string", text: value.slice(cursor, end) });
        cursor = end;
        continue;
      }

      const nextSpecial = [
        value.indexOf("${{", cursor),
        value.indexOf("#", cursor),
        value.indexOf('"', cursor),
        value.indexOf("'", cursor),
      ].filter((position) => position !== -1);
      const next =
        nextSpecial.length > 0 ? Math.min(...nextSpecial) : value.length;
      if (next > cursor) {
        tokens.push({ kind: "plain", text: value.slice(cursor, next) });
      }
      cursor = next;
    }
  };

  pushLeading();
  if (index >= line.length) {
    return tokens;
  }

  if (line[index] === "#") {
    tokens.push({ kind: "comment", text: line.slice(index) });
    return tokens;
  }

  const colon = line.indexOf(":", index);
  const beforeColon = colon === -1 ? "" : line.slice(index, colon);
  if (
    colon !== -1 &&
    beforeColon.length > 0 &&
    !beforeColon.includes(" ") &&
    !beforeColon.includes("${{")
  ) {
    tokens.push(
      { kind: "key", text: beforeColon },
      { kind: "punctuation", text: ":" }
    );
    pushValue(line.slice(colon + 1));
    return tokens;
  }

  pushValue(line.slice(index));
  return tokens;
};

export const YamlExample = ({
  className,
  value,
}: {
  className?: string;
  value: string;
}) => (
  <pre
    className={cn(
      "overflow-x-auto border border-border bg-muted/20 p-4 font-mono text-[10px] leading-5",
      className
    )}
  >
    {value.split("\n").map((line, lineIndex) => (
      <div key={`${lineIndex}:${line}`}>
        {line.length === 0 ? (
          <span>{"\n"}</span>
        ) : (
          tokenizeYamlLine(line).map((token, tokenIndex) => (
            <span
              className={yamlTokenClass[token.kind]}
              key={`${lineIndex}:${tokenIndex}:${token.kind}`}
            >
              {token.text}
            </span>
          ))
        )}
      </div>
    ))}
  </pre>
);

export const GitHubActionExampleDialog = ({
  description,
  example,
  notes,
  steps,
  title,
  triggerLabel = "Setup guide",
}: {
  description: string;
  example: string;
  notes?: ReactNode;
  steps: string[];
  title: string;
  triggerLabel?: string;
}) => {
  const [copied, setCopied] = useState(false);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(example);
      setCopied(true);
    } catch {
      setCopied(false);
    }
  };

  return (
    <Dialog.Root
      onOpenChange={(open) => {
        if (!open) {
          setCopied(false);
        }
      }}
    >
      <Dialog.Trigger
        render={
          <Button size="sm" type="button" variant="outline">
            <BookOpen /> {triggerLabel}
          </Button>
        }
      />
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-50 bg-black/55 backdrop-blur-[1px] data-open:animate-in data-open:fade-in data-closed:animate-out data-closed:fade-out" />
        <Dialog.Viewport className="fixed inset-0 z-50 grid place-items-center overflow-y-auto p-4">
          <Dialog.Popup className="flex max-h-[calc(100dvh-2rem)] w-full max-w-3xl flex-col border border-border bg-background text-foreground shadow-2xl data-open:animate-in data-open:zoom-in-95 data-open:fade-in data-closed:animate-out data-closed:zoom-out-95 data-closed:fade-out">
            <header className="flex items-start justify-between gap-5 border-b border-border px-5 py-4">
              <div>
                <Dialog.Title className="text-sm font-medium">
                  {title}
                </Dialog.Title>
                <Dialog.Description className="mt-1.5 max-w-2xl text-[10px] leading-4 text-muted-foreground">
                  {description}
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
              <section className="border-b border-border px-5 py-4">
                <h4 className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                  How it works
                </h4>
                <ol className="mt-3 grid gap-2">
                  {steps.map((step, index) => (
                    <li
                      className="grid grid-cols-[1.5rem_minmax(0,1fr)] gap-2 text-[10px] leading-4"
                      key={step}
                    >
                      <span className="text-muted-foreground tabular-nums">
                        {index + 1}.
                      </span>
                      <span>{step}</span>
                    </li>
                  ))}
                </ol>
              </section>

              <section className="px-5 py-4">
                <div className="flex items-center justify-between gap-3">
                  <h4 className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                    Example workflow
                  </h4>
                  <Button
                    aria-label="Copy example workflow"
                    onClick={() => void copy()}
                    size="sm"
                    type="button"
                    variant="outline"
                  >
                    {copied ? <Check /> : <Clipboard />}
                    {copied ? "Copied" : "Copy"}
                  </Button>
                </div>
                <YamlExample className="mt-3" value={example} />
              </section>
            </div>

            {notes ? (
              <footer className="border-t border-border bg-muted/15 px-5 py-3 text-[9px] leading-4 text-muted-foreground">
                {notes}
              </footer>
            ) : null}
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
};

export const adminOrigin = () => {
  if (typeof window === "undefined") {
    return "https://admin.example.com";
  }
  return window.location.origin;
};

export const projectNameFromInternalHostname = (hostname: string) => {
  const bare = hostname.trim().replace(/\.internal$/u, "");
  const parts = bare.split(".").filter(Boolean);
  if (parts.length < 2) {
    return "my-project";
  }
  return parts.slice(1).join(".");
};

export const uploadImageActionExample = ({
  origin = adminOrigin(),
  projectID,
  serviceID,
}: {
  origin?: string;
  projectID: string;
  serviceID: string;
}) => `# Build an OCI archive and upload it directly to this service.
permissions:
  contents: read
  id-token: write
  deployments: write

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: docker/setup-buildx-action@v4

      - uses: docker/build-push-action@v7
        with:
          context: .
          platforms: linux/amd64
          outputs: type=oci,dest=\${{ runner.temp }}/image.oci

      - uses: iivankin/platformd/actions/upload-image@v1
        with:
          # platformd admin origin — where the action calls the API (not the tunnel)
          url: ${origin}
          project: ${projectID}
          resource: ${serviceID}
          archive: \${{ runner.temp }}/image.oci
          # latest from the production branch deploys production.
          # Any other tag creates a preview that expires after 14 days.
          tag: latest`;

export type PortForwardExampleKind =
  | "object_store"
  | "postgres"
  | "redis"
  | "service";

export const portForwardActionExample = ({
  kind = "postgres",
  localPort,
  origin = adminOrigin(),
  port,
  projectName,
  resourceName,
}: {
  kind?: PortForwardExampleKind;
  localPort?: number;
  origin?: string;
  port: number;
  projectName: string;
  resourceName: string;
}) => {
  const local = localPort ?? port;
  if (kind === "postgres") {
    return `# Open a temporary tunnel to managed Postgres, then run migrations.
jobs:
  migrate:
    runs-on: ubuntu-24.04
    permissions:
      contents: read
      id-token: write # required for OIDC against the allowlist above
    steps:
      - uses: actions/checkout@v4

      - name: Tunnel to Postgres
        id: db
        uses: iivankin/platformd/actions/port-forward@v1
        with:
          # platformd admin origin — where the action calls the API (not the tunnel)
          url: ${origin}
          project: ${projectName}
          resource: ${resourceName}
          port: ${port}
          local-port: ${local}
          # Rewrites only host/port; keeps user, password, and database
          connection-url: \${{ secrets.POSTGRES_URL }}

      - name: Run migrations
        # Action exports POSTGRES_URL -> postgres://...@127.0.0.1:${local}/...
        run: bun run migrate`;
  }

  if (kind === "redis") {
    return `# Open a temporary tunnel to managed Redis for integration checks.
jobs:
  redis:
    runs-on: ubuntu-24.04
    permissions:
      contents: read
      id-token: write
    steps:
      - uses: actions/checkout@v4

      - name: Tunnel to Redis
        uses: iivankin/platformd/actions/port-forward@v1
        with:
          # platformd admin origin — where the action calls the API (not the tunnel)
          url: ${origin}
          project: ${projectName}
          resource: ${resourceName}
          port: ${port}
          local-port: ${local}
          connection-url: \${{ secrets.REDIS_URL }}

      - name: Smoke test
        # Action exports REDIS_URL -> redis://...@127.0.0.1:${local}
        run: bun run test:redis`;
  }

  if (kind === "object_store") {
    return `# Forward the project S3 endpoint (always port 9000) to localhost.
jobs:
  storage:
    runs-on: ubuntu-24.04
    permissions:
      contents: read
      id-token: write
    steps:
      - uses: actions/checkout@v4

      - name: Tunnel to object storage
        uses: iivankin/platformd/actions/port-forward@v1
        with:
          # platformd admin origin — where the action calls the API (not the tunnel)
          url: ${origin}
          project: ${projectName}
          resource: ${resourceName}
          port: 9000
          local-port: ${local}

      - name: Upload fixture
        env:
          AWS_ACCESS_KEY_ID: \${{ secrets.S3_ACCESS_KEY_ID }}
          AWS_SECRET_ACCESS_KEY: \${{ secrets.S3_SECRET_ACCESS_KEY }}
          AWS_ENDPOINT_URL: http://127.0.0.1:${local}
        run: aws s3 cp ./fixture.bin s3://bucket/fixture.bin`;
  }

  return `# Forward a service TCP port to the runner for integration tests.
jobs:
  integration:
    runs-on: ubuntu-24.04
    permissions:
      contents: read
      id-token: write
    steps:
      - uses: actions/checkout@v4

      - name: Tunnel to service
        id: tunnel
        uses: iivankin/platformd/actions/port-forward@v1
        with:
          # platformd admin origin — where the action calls the API (not the tunnel)
          url: ${origin}
          project: ${projectName}
          resource: ${resourceName}
          port: ${port}
          local-port: ${local}

      - name: Hit health check
        run: curl -fsS "http://127.0.0.1:\${{ steps.tunnel.outputs.port }}/health"`;
};
