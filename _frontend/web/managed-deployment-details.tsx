import type { RuntimeDeployment } from "@/api";

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

const formatDuration = (deployment: RuntimeDeployment) => {
  if (!deployment.finishedAt) {
    return "In progress";
  }
  const milliseconds = Math.max(
    0,
    deployment.finishedAt - deployment.createdAt
  );
  return milliseconds < 1000
    ? `${milliseconds.toString()} ms`
    : `${(milliseconds / 1000).toFixed(1)} seconds`;
};

export const ManagedDeploymentDetails = ({
  deployment,
}: {
  deployment: RuntimeDeployment;
}) => (
  <div>
    {deployment.errorMessage ? (
      <section className="border-b border-destructive/40 bg-destructive/5 px-5 py-4 text-[10px] text-destructive">
        <p className="font-medium">
          {deployment.errorCode || "Deployment failed"}
        </p>
        <p className="mt-1 leading-4">{deployment.errorMessage}</p>
      </section>
    ) : null}
    <h3 className="border-b border-border bg-muted/10 px-5 py-2.5 text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
      Lifecycle
    </h3>
    <dl>
      <Detail label="Status" value={deployment.status} />
      <Detail
        label="Current deployment"
        value={deployment.active ? "Yes" : "No"}
      />
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
    <h3 className="border-y border-border bg-muted/10 px-5 py-2.5 text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
      Runtime image
    </h3>
    <dl>
      <Detail label="Deployment ID" value={deployment.id} />
      <Detail
        label="Image"
        value={`${deployment.resourceKind}:${deployment.imageTag}`}
      />
      <Detail label="Image digest" value={deployment.imageDigest} />
    </dl>
  </div>
);
