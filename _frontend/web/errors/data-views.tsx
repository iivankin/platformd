import { ArrowUpRight } from "lucide-react";

import { Button } from "@/components/ui/button";

import { EmptyView, StatusBadge } from "./common-ui";
import { formatBytes, formatTime, shortId } from "./format";
import type { DetailTarget, Issue, StoredDocument } from "./types";

const Head = ({
  children,
  className = "",
}: {
  children: string;
  className?: string;
}) => (
  <th
    className={`h-9 border-b border-border px-4 text-left text-[9px] font-normal tracking-[0.08em] text-muted-foreground uppercase ${className}`}
  >
    {children}
  </th>
);

const Cell = ({
  children,
  className = "",
}: {
  children: React.ReactNode;
  className?: string;
}) => (
  <td
    className={`h-14 border-b border-border/70 px-4 text-[10px] text-muted-foreground ${className}`}
  >
    {children}
  </td>
);

const TitleButton = ({
  copy,
  onClick,
  title,
}: {
  copy?: string;
  onClick: () => void;
  title: string;
}) => (
  <Button
    className="h-auto max-w-full justify-start gap-2 p-0 text-left text-foreground hover:bg-transparent hover:text-foreground/75"
    onClick={onClick}
    variant="ghost"
  >
    <span className="min-w-0">
      <span className="block overflow-hidden text-xs font-medium text-ellipsis whitespace-nowrap">
        {title}
      </span>
      {copy ? (
        <span className="mt-1 block overflow-hidden text-[9px] font-normal text-ellipsis whitespace-nowrap text-muted-foreground">
          {copy}
        </span>
      ) : null}
    </span>
    <ArrowUpRight className="ml-auto size-3 shrink-0 opacity-45" />
  </Button>
);

export const IssuesView = ({
  items,
  openDetail,
}: {
  items: Issue[];
  openDetail: (target: DetailTarget) => void;
}) => {
  if (items.length === 0) {
    return (
      <EmptyView
        copy="New error groups will appear here as SDK events arrive."
        title="No issues"
      />
    );
  }
  return (
    <table className="w-full table-fixed border-collapse">
      <thead>
        <tr>
          <Head className="w-[46%]">Issue</Head>
          <Head>Status</Head>
          <Head className="hidden md:table-cell">Platform</Head>
          <Head>Events</Head>
          <Head className="hidden lg:table-cell">Last seen</Head>
        </tr>
      </thead>
      <tbody>
        {items.map((item) => (
          <tr className="hover:bg-muted/35" key={item.id}>
            <Cell>
              <TitleButton
                copy={shortId(item.id, 20)}
                onClick={() => openDetail({ id: item.id, kind: "issue" })}
                title={item.title}
              />
            </Cell>
            <Cell>
              <StatusBadge value={item.status} />
            </Cell>
            <Cell className="hidden md:table-cell">{item.platform || "—"}</Cell>
            <Cell>{item.eventCount.toLocaleString()}</Cell>
            <Cell className="hidden lg:table-cell">
              {formatTime(item.lastSeen)}
            </Cell>
          </tr>
        ))}
      </tbody>
    </table>
  );
};

export const ArtifactsView = ({ items }: { items: StoredDocument[] }) => {
  if (items.length === 0) {
    return (
      <EmptyView
        copy="Upload source maps, artifact bundles, ProGuard mappings, dSYMs, PDBs, or ELF debug files with sentry-cli."
        title="No debug artifacts"
      />
    );
  }
  return (
    <table className="w-full table-fixed border-collapse">
      <thead>
        <tr>
          <Head className="w-[42%]">Artifact</Head>
          <Head>Type</Head>
          <Head className="hidden md:table-cell">Debug ID</Head>
          <Head>Size</Head>
          <Head className="hidden lg:table-cell">Uploaded</Head>
        </tr>
      </thead>
      <tbody>
        {items.map((item) => (
          <tr
            className="hover:bg-muted/35"
            key={item.checksum ?? item.content_id ?? item.timestamp}
          >
            <Cell className="text-foreground">
              <span className="block overflow-hidden text-xs font-medium text-ellipsis whitespace-nowrap">
                {item.filename ??
                  item.object_name ??
                  item.checksum ??
                  "Artifact"}
              </span>
              <span className="mt-1 block text-[9px] text-muted-foreground">
                {shortId(item.checksum, 20)}
              </span>
            </Cell>
            <Cell>
              <StatusBadge value={item.symbol_type ?? item.item_type} />
            </Cell>
            <Cell className="hidden md:table-cell">
              {item.debug_id ?? item.code_id ?? "—"}
            </Cell>
            <Cell>{formatBytes(item.size_bytes)}</Cell>
            <Cell className="hidden lg:table-cell">
              {formatTime(item.timestamp)}
            </Cell>
          </tr>
        ))}
      </tbody>
    </table>
  );
};
