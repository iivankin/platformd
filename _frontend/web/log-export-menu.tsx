import { Check, Copy, Download } from "lucide-react";
import { useState } from "react";

import type { LogRecord } from "@/api";
import { Button } from "@/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import {
  formatLogExport,
  logExportExtension,
  logExportMime,
} from "@/log-export";
import type { LogExportFormat } from "@/log-export";

const formats: { format: LogExportFormat; label: string }[] = [
  { format: "text", label: "Plain text" },
  { format: "json", label: "JSON" },
  { format: "csv", label: "CSV" },
];

const filename = (resourceID: string, format: LogExportFormat) => {
  const timestamp = new Date().toISOString().replaceAll(/[:.]/gu, "-");
  const safeResourceID = resourceID.replaceAll(/[^A-Za-z0-9_-]/gu, "-");
  return `${safeResourceID}-logs-${timestamp}.${logExportExtension(format)}`;
};

const save = (content: string, name: string, mime: string) => {
  const url = URL.createObjectURL(new Blob([content], { type: mime }));
  const anchor = document.createElement("a");
  anchor.download = name;
  anchor.href = url;
  anchor.click();
  setTimeout(() => URL.revokeObjectURL(url), 0);
};

const ExportAction = ({
  format,
  label,
  onClick,
}: {
  format: LogExportFormat;
  label: string;
  onClick: (format: LogExportFormat) => void;
}) => (
  <button
    className="flex h-8 w-full items-center px-2 text-left text-[10px] text-muted-foreground hover:bg-muted/60 hover:text-foreground focus-visible:bg-muted/60 focus-visible:text-foreground focus-visible:outline-none"
    onClick={() => onClick(format)}
    type="button"
  >
    {label}
  </button>
);

export const LogExportMenu = ({
  records,
  resourceID,
}: {
  records: LogRecord[];
  resourceID: string;
}) => {
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState<LogExportFormat>();

  const download = (format: LogExportFormat) => {
    const content = formatLogExport(records, format);
    save(content, filename(resourceID, format), logExportMime(format));
    setOpen(false);
  };

  const copy = async (format: LogExportFormat) => {
    await navigator.clipboard.writeText(formatLogExport(records, format));
    setCopied(format);
    setTimeout(() => setCopied(undefined), 1500);
  };

  return (
    <Popover onOpenChange={setOpen} open={open}>
      <PopoverTrigger
        render={
          <Button
            aria-label="Export logs"
            disabled={records.length === 0}
            size="icon"
            variant="ghost"
          >
            <Download />
          </Button>
        }
      />
      <PopoverContent align="end" className="w-60 gap-0 p-2">
        <div className="border-b border-border px-2 pt-1 pb-2">
          <p className="text-[10px] font-medium">Export visible logs</p>
          <p className="mt-0.5 text-[8px] text-muted-foreground tabular-nums">
            {records.length.toLocaleString()} filtered records
          </p>
        </div>
        <div className="py-1">
          <p className="px-2 py-1 text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
            Download as
          </p>
          {formats.map(({ format, label }) => (
            <ExportAction
              format={format}
              key={`download:${format}`}
              label={label}
              onClick={download}
            />
          ))}
        </div>
        <div className="border-t border-border pt-1">
          <p className="px-2 py-1 text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
            Copy as
          </p>
          {formats.map(({ format, label }) => (
            <button
              className="flex h-8 w-full items-center justify-between px-2 text-left text-[10px] text-muted-foreground hover:bg-muted/60 hover:text-foreground focus-visible:bg-muted/60 focus-visible:text-foreground focus-visible:outline-none"
              key={`copy:${format}`}
              onClick={() => void copy(format)}
              type="button"
            >
              {label}
              {copied === format ? (
                <Check className="size-3 text-emerald-500" />
              ) : (
                <Copy className="size-3 opacity-50" />
              )}
            </button>
          ))}
        </div>
      </PopoverContent>
    </Popover>
  );
};
