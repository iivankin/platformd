import { Fragment } from "react";

import { demoCanvasPresets } from "@/project-canvas-demo";
import type { DemoCanvasPreset } from "@/project-canvas-demo";

const groupLabels = {
  examples: "Examples",
  stress: "Stress",
} as const;

export const ProjectCanvasDemoSwitcher = ({
  onChange,
  value,
}: {
  onChange: (preset: DemoCanvasPreset) => void;
  value: DemoCanvasPreset;
}) => (
  <fieldset
    aria-label="Demo canvas scenario"
    className="absolute top-4 left-5 z-10 m-0 flex h-7 max-w-[calc(100%-12rem)] overflow-x-auto border border-border bg-background p-0 shadow-sm"
  >
    <legend className="sr-only">Demo canvas scenario</legend>
    <span className="flex shrink-0 items-center border-r border-border px-2.5 text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
      Demo graph
    </span>
    {demoCanvasPresets.map((preset, index) => {
      const active = preset.value === value;
      const previousGroup = demoCanvasPresets[index - 1]?.group;
      const groupLabel =
        preset.group === "current" || preset.group === previousGroup
          ? undefined
          : groupLabels[preset.group];
      return (
        <Fragment key={preset.value}>
          {groupLabel ? (
            <span className="flex shrink-0 items-center border-r border-border bg-muted/30 px-2 text-[7px] tracking-[0.12em] text-muted-foreground uppercase">
              {groupLabel}
            </span>
          ) : null}
          <button
            aria-pressed={active}
            className={`shrink-0 border-r border-border px-2.5 text-[9px] transition-colors last:border-r-0 ${
              active
                ? "bg-foreground text-background"
                : "text-muted-foreground hover:bg-muted hover:text-foreground"
            }`}
            onClick={() => onChange(preset.value)}
            type="button"
          >
            {preset.label}
          </button>
        </Fragment>
      );
    })}
  </fieldset>
);
