import { LoaderCircle, Play } from "lucide-react";

import type {
  ServiceMetricDescriptor,
  ServiceMetricSqlRow,
  ServiceMetricVisualization,
} from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { FormLabel } from "@/errors/common-ui";
import { metricQueryTemplates, metricSqlColumns } from "@/service-metric-model";
import type { ServiceMetricChartDraft } from "@/service-metric-model";

const visualizations: {
  label: string;
  value: ServiceMetricVisualization;
}[] = [
  { label: "Area", value: "area" },
  { label: "Line", value: "line" },
  { label: "Bars", value: "bar" },
  { label: "Value", value: "value" },
];

export const ServiceMetricChartFields = ({
  catalog,
  draft,
  fieldID,
  onChange,
  onPreview,
  previewError,
  previewPending,
  previewRows,
}: {
  catalog: ServiceMetricDescriptor[];
  draft: ServiceMetricChartDraft;
  fieldID: string;
  onChange: (draft: ServiceMetricChartDraft) => void;
  onPreview: () => void;
  previewError: string;
  previewPending: boolean;
  previewRows: ServiceMetricSqlRow[];
}) => {
  const templates = metricQueryTemplates(catalog[0]);
  return (
    <div>
      <div className="grid gap-4 px-5 py-5 sm:grid-cols-[minmax(0,1fr)_18rem]">
        <div>
          <FormLabel htmlFor={`metric-chart-title-${fieldID}`}>Title</FormLabel>
          <Input
            id={`metric-chart-title-${fieldID}`}
            maxLength={80}
            onChange={(event) =>
              onChange({ ...draft, title: event.target.value })
            }
            placeholder="Checkout error rate"
            required
            value={draft.title}
          />
        </div>
        <div>
          <FormLabel htmlFor={`metric-visualization-${fieldID}`}>
            Visualization
          </FormLabel>
          <div className="grid grid-cols-4 border border-border">
            {visualizations.map((visualization) => (
              <button
                className={`h-9 border-r border-border text-[9px] last:border-r-0 ${
                  draft.visualization === visualization.value
                    ? "bg-foreground text-background"
                    : "text-muted-foreground hover:bg-muted hover:text-foreground"
                }`}
                id={
                  visualization.value === "area"
                    ? `metric-visualization-${fieldID}`
                    : undefined
                }
                key={visualization.value}
                onClick={() =>
                  onChange({
                    ...draft,
                    visualization: visualization.value,
                  })
                }
                type="button"
              >
                {visualization.label}
              </button>
            ))}
          </div>
        </div>
      </div>

      <div className="grid border-y border-border lg:grid-cols-[minmax(0,1fr)_22rem]">
        <div className="min-w-0 px-5 py-5">
          <div className="flex flex-wrap items-end gap-2">
            <div className="mr-auto">
              <FormLabel htmlFor={`metric-sql-${fieldID}`}>
                ClickHouse SQL
              </FormLabel>
              <p className="text-[9px] leading-4 text-muted-foreground">
                Read from <code>metrics</code>. Return <code>time</code>,{" "}
                <code>value</code>, and optional <code>series</code>.
              </p>
            </div>
            {templates.map((template) => (
              <Button
                key={template.label}
                onClick={() => onChange({ ...draft, sql: template.sql })}
                size="sm"
                type="button"
                variant="ghost"
              >
                {template.label}
              </Button>
            ))}
          </div>
          <textarea
            autoCapitalize="off"
            autoCorrect="off"
            className="mt-3 min-h-72 w-full resize-y border border-border bg-background px-3 py-3 font-mono text-[10px] leading-5 outline-none focus:border-foreground"
            id={`metric-sql-${fieldID}`}
            maxLength={16_384}
            onChange={(event) =>
              onChange({ ...draft, sql: event.target.value })
            }
            required
            spellCheck={false}
            value={draft.sql}
          />
          <div className="mt-3 flex items-center gap-3">
            <Button
              disabled={previewPending || !draft.sql.trim()}
              onClick={onPreview}
              type="button"
              variant="outline"
            >
              {previewPending ? (
                <LoaderCircle className="animate-spin" />
              ) : (
                <Play />
              )}
              Run preview
            </Button>
            <p className="text-[9px] text-muted-foreground">
              Preview uses the last hour at 20-second resolution.
            </p>
          </div>
          {previewError ? (
            <p className="mt-3 border-l border-destructive px-3 text-[9px] leading-4 text-destructive">
              {previewError}
            </p>
          ) : null}
          {previewRows.length ? (
            <div className="mt-4 max-h-44 overflow-auto border-y border-border text-[9px]">
              <div className="grid grid-cols-[minmax(11rem,1fr)_minmax(8rem,1fr)_minmax(7rem,1fr)] border-b border-border px-3 py-2 text-muted-foreground uppercase">
                <span>Time</span>
                <span>Value</span>
                <span>Series</span>
              </div>
              {previewRows.slice(0, 8).map((row, index) => (
                <div
                  className="grid grid-cols-[minmax(11rem,1fr)_minmax(8rem,1fr)_minmax(7rem,1fr)] border-b border-border/70 px-3 py-2 last:border-b-0"
                  key={`${row.timeUnixNano}-${row.series ?? "value"}-${index}`}
                >
                  <span>{row.timeUnixNano}</span>
                  <span>{row.value}</span>
                  <span className="text-muted-foreground">
                    {row.series ?? "value"}
                  </span>
                </div>
              ))}
            </div>
          ) : null}
        </div>

        <aside className="min-w-0 border-t border-border px-5 py-5 lg:border-t-0 lg:border-l">
          <h3 className="text-[10px] font-medium">metrics schema</h3>
          <p className="mt-1 text-[9px] leading-4 text-muted-foreground">
            This logical table is restricted to the selected UI scope and time
            range. Cumulative deltas and histogram buckets are prepared by chDB.
          </p>
          <div className="mt-4 max-h-72 overflow-auto border-y border-border">
            {metricSqlColumns.map(([name, type, description]) => (
              <div
                className="grid grid-cols-[minmax(0,1fr)_auto] gap-2 border-b border-border/70 px-2 py-2 last:border-b-0"
                key={name}
                title={description}
              >
                <code className="truncate text-[9px]">{name}</code>
                <span className="text-[8px] text-muted-foreground">{type}</span>
              </div>
            ))}
          </div>
          <h3 className="mt-5 text-[10px] font-medium">Observed metrics</h3>
          <div className="mt-2 max-h-36 overflow-auto border-y border-border">
            {catalog.length ? (
              catalog.map((metric) => (
                <button
                  className="flex w-full items-center justify-between gap-3 border-b border-border/70 px-2 py-2 text-left last:border-b-0 hover:bg-muted"
                  key={metric.name}
                  onClick={() => {
                    const [template] = metricQueryTemplates(metric);
                    if (template) {
                      onChange({
                        ...draft,
                        sql: template.sql,
                        title: draft.title || metric.name,
                        unit: draft.unit || metric.unit || undefined,
                      });
                    }
                  }}
                  type="button"
                >
                  <code className="truncate text-[9px]">{metric.name}</code>
                  <span className="shrink-0 text-[8px] text-muted-foreground">
                    {metric.kind}
                  </span>
                </button>
              ))
            ) : (
              <p className="px-2 py-3 text-[9px] text-muted-foreground">
                No application metrics yet.
              </p>
            )}
          </div>
        </aside>
      </div>

      <div className="grid gap-4 px-5 py-5 sm:grid-cols-2">
        <div>
          <FormLabel htmlFor={`metric-legend-${fieldID}`}>
            Legend label
          </FormLabel>
          <Input
            id={`metric-legend-${fieldID}`}
            maxLength={80}
            onChange={(event) =>
              onChange({ ...draft, legend: event.target.value })
            }
            placeholder="SQL series value"
            value={draft.legend}
          />
        </div>
        <div>
          <FormLabel htmlFor={`metric-unit-${fieldID}`}>Unit</FormLabel>
          <Input
            id={`metric-unit-${fieldID}`}
            maxLength={32}
            onChange={(event) =>
              onChange({ ...draft, unit: event.target.value || undefined })
            }
            placeholder="%, ms, requests/s"
            value={draft.unit ?? ""}
          />
        </div>
      </div>
    </div>
  );
};
