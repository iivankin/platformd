import { SectionCard } from "@/components/ui/card";
import { PageStack } from "@/components/ui/page-stack";
import type { PendingResourceCreation } from "@/pending-resource-creation";

type ManagedDraft = Exclude<
  PendingResourceCreation,
  { kind: "network_gateway" | "service" }
>;

interface DraftVariable {
  name: string;
  value: string;
}

const postgresURL = (
  hostname: string,
  databaseName: string,
  username: string,
  password: string
) =>
  `postgresql://${encodeURIComponent(username)}:${encodeURIComponent(password)}@${hostname}:5432/${encodeURIComponent(databaseName)}`;

const redisURL = (hostname: string, password: string) =>
  `redis://:${encodeURIComponent(password)}@${hostname}:6379/0`;

const draftVariables = (
  draft: ManagedDraft,
  projectName: string
): DraftVariable[] => {
  const hostname = `${draft.input.name}.${projectName}.internal`;
  switch (draft.kind) {
    case "postgres": {
      const { databaseName, ownerPassword, ownerUsername } =
        draft.input.credentials;
      const connection = postgresURL(
        hostname,
        databaseName,
        ownerUsername,
        ownerPassword
      );
      return [
        { name: "PGHOST", value: hostname },
        { name: "PGPORT", value: "5432" },
        { name: "PGDATABASE", value: databaseName },
        { name: "PGUSER", value: ownerUsername },
        { name: "PGPASSWORD", value: ownerPassword },
        { name: "DATABASE_URL", value: connection },
        { name: "POSTGRES_URL", value: connection },
      ];
    }
    case "redis": {
      const { password } = draft.input.credentials;
      return [
        { name: "REDISHOST", value: hostname },
        { name: "REDISPORT", value: "6379" },
        { name: "REDISPASSWORD", value: password },
        { name: "REDIS_URL", value: redisURL(hostname, password) },
      ];
    }
    case "storage": {
      const { accessKey, secret } = draft.input.credentials;
      return [
        { name: "S3_ENDPOINT", value: `http://${hostname}:9000` },
        { name: "S3_REGION", value: "us-east-1" },
        { name: "S3_BUCKET", value: draft.input.bucketName },
        { name: "S3_ACCESS_KEY_ID", value: accessKey },
        { name: "S3_SECRET_ACCESS_KEY", value: secret },
      ];
    }
    default: {
      return [];
    }
  }
};

export const ResourceDraftVariables = ({
  draft,
  projectName,
}: {
  draft: ManagedDraft;
  projectName: string;
}) => {
  const variables = draftVariables(draft, projectName);
  return (
    <PageStack>
      <SectionCard>
        <header className="border-b border-border px-5 py-4">
          <h3 className="text-[10px] font-medium">Exported variables</h3>
          <p className="mt-1 text-[9px] leading-4 text-muted-foreground">
            These values are generated with the draft and stay unchanged when it
            is deployed.
          </p>
        </header>
        <div className="grid grid-cols-[13rem_minmax(0,1fr)] border-b border-border px-5 py-2 text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
          <span>Name</span>
          <span>Draft value</span>
        </div>
        {variables.map((variable) => (
          <div
            className="grid min-h-11 grid-cols-[13rem_minmax(0,1fr)] items-center border-b border-border px-5 text-[10px]"
            key={variable.name}
          >
            <code>{variable.name}</code>
            <code className="truncate text-muted-foreground">
              {variable.value}
            </code>
          </div>
        ))}
      </SectionCard>
    </PageStack>
  );
};
