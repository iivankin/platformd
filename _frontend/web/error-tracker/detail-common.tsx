import { Download, LoaderCircle } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/ui/button";

import { downloadContent } from "./api";
import { errorMessage, saveBlob } from "./format";

export const DetailGrid = ({ rows }: { rows: [string, React.ReactNode][] }) => (
  <dl className="grid grid-cols-[112px_minmax(0,1fr)] gap-x-4 gap-y-2 text-[10px]">
    {rows.map(([label, value]) => (
      <div className="contents" key={label}>
        <dt className="text-[9px] tracking-[0.06em] text-muted-foreground uppercase">
          {label}
        </dt>
        <dd className="m-0 min-w-0 [overflow-wrap:anywhere] text-foreground/80">
          {value || "—"}
        </dd>
      </div>
    ))}
  </dl>
);

export const DownloadButton = ({
  appId,
  contentId,
  filename,
  onError,
}: {
  appId: string;
  contentId: string;
  filename: string;
  onError: (message: string) => void;
}) => {
  const [pending, setPending] = useState(false);
  const download = async () => {
    setPending(true);
    try {
      saveBlob(await downloadContent(appId, contentId), filename);
    } catch (error) {
      onError(errorMessage(error, "Download failed"));
    } finally {
      setPending(false);
    }
  };
  return (
    <Button
      disabled={pending}
      onClick={() => void download()}
      size="sm"
      variant="outline"
    >
      {pending ? <LoaderCircle className="animate-spin" /> : <Download />}
      Download raw
    </Button>
  );
};
