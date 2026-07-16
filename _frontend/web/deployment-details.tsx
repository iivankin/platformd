import type { Deployment } from "@/api";

const formatBytes = (bytes?: number) => {
  if (!bytes) {
    return "Unlimited";
  }
  let value = bytes;
  let unit = "B";
  for (const candidate of ["KiB", "MiB", "GiB"]) {
    if (value < 1024) {
      break;
    }
    value /= 1024;
    unit = candidate;
  }
  return `${value.toFixed(value >= 10 ? 0 : 1)} ${unit}`;
};

const formatDuration = (deployment: Deployment) => {
  if (!deployment.finishedAt) {
    return "In progress";
  }
  const milliseconds = Math.max(
    0,
    deployment.finishedAt - deployment.createdAt
  );
  if (milliseconds < 1000) {
    return `${milliseconds.toString()} ms`;
  }
  return `${(milliseconds / 1000).toFixed(1)} seconds`;
};

const Detail = ({ label, value }: { label: string; value: string }) => (
  <div className="grid gap-1 border-b border-border px-5 py-3 last:border-b-0 md:grid-cols-[10rem_minmax(0,1fr)] md:gap-4">
    <dt className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
      {label}
    </dt>
    <dd className="min-w-0 font-mono text-[10px] leading-4 break-all">
      {value}
    </dd>
  </div>
);

const SectionTitle = ({ children }: { children: string }) => (
  <h3 className="border-b border-border bg-muted/10 px-5 py-2.5 text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
    {children}
  </h3>
);

export const DeploymentDetails = ({
  deployment,
}: {
  deployment: Deployment;
}) => {
  const { snapshot } = deployment;
  const variables = [
    ...Object.entries(snapshot.environment).map(([name, value]) => ({
      name,
      source: value.includes("${{") ? "Expression" : "Literal",
    })),
    ...snapshot.secretReferences.map((reference) => ({
      name: reference.environmentName,
      source: `Secret / ${reference.secretId}`,
    })),
  ].toSorted((left, right) => left.name.localeCompare(right.name));

  return (
    <div>
      {deployment.errorMessage ? (
        <section className="border-b border-destructive/40 bg-destructive/5 px-5 py-4 text-[10px] text-destructive">
          <p className="font-medium">
            {deployment.errorCode || "Deployment failed"}
          </p>
          <p className="mt-1 leading-4">{deployment.errorMessage}</p>
        </section>
      ) : null}

      <section>
        <SectionTitle>Lifecycle</SectionTitle>
        <dl>
          <Detail label="Status" value={deployment.status} />
          <Detail
            label="Started"
            value={new Date(deployment.createdAt).toLocaleString()}
          />
          <Detail
            label="Finished"
            value={
              deployment.finishedAt
                ? new Date(deployment.finishedAt).toLocaleString()
                : "—"
            }
          />
          <Detail label="Duration" value={formatDuration(deployment)} />
        </dl>
      </section>

      <section>
        <SectionTitle>Immutable deployment</SectionTitle>
        <dl>
          <Detail label="Deployment ID" value={deployment.id} />
          <Detail label="Image digest" value={deployment.imageDigest} />
          <Detail
            label="Configuration hash"
            value={deployment.serviceConfigHash}
          />
        </dl>
      </section>

      <section>
        <SectionTitle>Runtime snapshot</SectionTitle>
        <dl>
          <Detail label="Image" value={snapshot.imageReference} />
          <Detail
            label="Command"
            value={snapshot.command?.join(" ") || "Image default"}
          />
          <Detail
            label="Arguments"
            value={snapshot.args?.join(" ") || "None"}
          />
          <Detail
            label="Health check"
            value={
              snapshot.healthCheck
                ? `HTTP :${snapshot.healthCheck.port.toString()}${snapshot.healthCheck.path}`
                : "Off"
            }
          />
          <Detail
            label="CPU limit"
            value={
              snapshot.cpuMillicores
                ? `${snapshot.cpuMillicores.toString()}m`
                : "Unlimited"
            }
          />
          <Detail
            label="Memory limit"
            value={formatBytes(snapshot.memoryMaxBytes)}
          />
          {snapshot.healthCheck ? (
            <Detail
              label="Health timeout"
              value={`${snapshot.healthCheck.timeoutSeconds.toString()} seconds`}
            />
          ) : null}
        </dl>
      </section>

      <section>
        <SectionTitle>Variables at deploy time</SectionTitle>
        {variables.length ? (
          <div className="divide-y divide-border">
            {variables.map((variable) => (
              <div
                className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)] gap-4 px-5 py-3 text-[10px]"
                key={`${variable.name}:${variable.source}`}
              >
                <code className="truncate">{variable.name}</code>
                <span className="truncate text-muted-foreground">
                  {variable.source}
                </span>
              </div>
            ))}
          </div>
        ) : (
          <p className="border-b border-border px-5 py-6 text-[10px] text-muted-foreground">
            No variables were configured for this deployment.
          </p>
        )}
      </section>
    </div>
  );
};
