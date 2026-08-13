import { Plus, SlidersHorizontal } from "lucide-react";
import { useState } from "react";
import type { FormEvent, ReactElement } from "react";

import { createMetricChart, fetchMetricQuery, updateMetricChart } from "@/api";
import type {
  MetricScope,
  ServiceMetricChart,
  ServiceMetricDescriptor,
  ServiceMetricSqlRow,
} from "@/api";
import { Button } from "@/components/ui/button";
import { FormFooter, Modal } from "@/errors/dialog-frame";
import { ServiceMetricChartFields } from "@/service-metric-chart-fields";
import { emptyMetricChartDraft } from "@/service-metric-model";
import type { ServiceMetricChartDraft } from "@/service-metric-model";

const initialDraft = (
  chart: ServiceMetricChart | undefined,
  catalog: ServiceMetricDescriptor[]
): ServiceMetricChartDraft =>
  chart
    ? {
        legend: chart.legend,
        sql: chart.sql,
        title: chart.title,
        unit: chart.unit,
        visualization: chart.visualization,
      }
    : emptyMetricChartDraft(catalog[0]);

export const ServiceMetricChartDialog = ({
  catalog,
  chart,
  onSaved,
  scope,
  trigger,
}: {
  catalog: ServiceMetricDescriptor[];
  chart?: ServiceMetricChart;
  onSaved: (chart: ServiceMetricChart) => void;
  scope: MetricScope;
  trigger?: ReactElement;
}) => {
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState<ServiceMetricChartDraft>(() =>
    initialDraft(chart, catalog)
  );
  const [pending, setPending] = useState(false);
  const [saveErrorMessage, setSaveErrorMessage] = useState("");
  const [previewPending, setPreviewPending] = useState(false);
  const [previewError, setPreviewError] = useState("");
  const [previewRows, setPreviewRows] = useState<ServiceMetricSqlRow[]>([]);
  const fieldID = chart?.id ?? "new";

  const runPreview = async () => {
    setPreviewPending(true);
    setPreviewError("");
    const to = Date.now();
    try {
      const rows = await fetchMetricQuery(scope, {
        from: to - 60 * 60_000,
        sql: draft.sql,
        step: 20_000,
        to,
      });
      setPreviewRows(rows);
      return true;
    } catch (error) {
      setPreviewRows([]);
      setPreviewError(
        error instanceof Error ? error.message : "Unable to run metric SQL"
      );
      return false;
    } finally {
      setPreviewPending(false);
    }
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setPending(true);
    setSaveErrorMessage("");
    try {
      if (!(await runPreview())) {
        return;
      }
      const saved = chart
        ? await updateMetricChart(scope, chart.id, {
            ...draft,
            expectedUpdatedAt: chart.updatedAt,
          })
        : await createMetricChart(scope, draft);
      onSaved(saved);
      setOpen(false);
    } catch (saveError) {
      setSaveErrorMessage(
        saveError instanceof Error
          ? saveError.message
          : "Unable to save metric graph"
      );
    } finally {
      setPending(false);
    }
  };

  const reset = () => {
    setDraft(initialDraft(chart, catalog));
    setSaveErrorMessage("");
    setPreviewError("");
    setPreviewRows([]);
  };

  return (
    <Modal
      className="max-w-6xl"
      description="Query the scoped metrics table with ClickHouse SQL. The query is validated and runs inside the embedded telemetry database."
      onOpenChange={(nextOpen) => {
        setOpen(nextOpen);
        if (!nextOpen) {
          reset();
        }
      }}
      open={open}
      title={chart ? "Edit metric graph" : "Add custom metric graph"}
      trigger={
        trigger ?? (
          <Button>
            <Plus /> Add custom graph
          </Button>
        )
      }
    >
      <form onSubmit={submit}>
        <ServiceMetricChartFields
          catalog={catalog}
          draft={draft}
          fieldID={fieldID}
          onChange={(nextDraft) => {
            setDraft(nextDraft);
            setPreviewError("");
            setPreviewRows([]);
          }}
          onPreview={() => void runPreview()}
          previewError={previewError}
          previewPending={previewPending}
          previewRows={previewRows}
        />
        <FormFooter
          error={saveErrorMessage}
          label={chart ? "Save graph" : "Add graph"}
          pending={pending}
        />
      </form>
    </Modal>
  );
};

export const EditMetricChartButton = ({
  catalog,
  chart,
  onSaved,
  scope,
}: Omit<Parameters<typeof ServiceMetricChartDialog>[0], "trigger"> & {
  chart: ServiceMetricChart;
}) => (
  <ServiceMetricChartDialog
    catalog={catalog}
    chart={chart}
    onSaved={onSaved}
    scope={scope}
    trigger={
      <Button aria-label={`Edit ${chart.title}`} size="icon" variant="ghost">
        <SlidersHorizontal />
      </Button>
    }
  />
);
