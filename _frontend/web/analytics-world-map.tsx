import { useMemo, useRef, useState } from "react";
import type { PointerEvent as ReactPointerEvent } from "react";

import { formatCount } from "@/analytics-model";
import {
  analyticsWorldCountries,
  analyticsWorldViewBox,
} from "@/analytics-world-paths";

export const analyticsCountryName = (code: string) => {
  try {
    return new Intl.DisplayNames(["en"], { type: "region" }).of(code) ?? code;
  } catch {
    return code;
  }
};

export const AnalyticsWorldMap = ({
  onSelect,
  rows,
}: {
  onSelect: (country: string) => void;
  rows: { label: string; visitors: number }[];
}) => {
  const rootRef = useRef<HTMLDivElement>(null);
  const [hover, setHover] = useState<{
    code: string;
    visitors: number;
    width: number;
    x: number;
    y: number;
  }>();
  const byCode = useMemo(
    () => new Map(rows.map((row) => [row.label.toUpperCase(), row.visitors])),
    [rows]
  );
  const maximum = Math.max(1, ...rows.map((row) => row.visitors));

  const moveHover = (
    event: ReactPointerEvent<SVGPathElement>,
    code: string,
    visitors: number
  ) => {
    const bounds = rootRef.current?.getBoundingClientRect();
    if (!bounds) {
      return;
    }
    setHover({
      code,
      visitors,
      width: bounds.width,
      x: event.clientX - bounds.left,
      y: event.clientY - bounds.top,
    });
  };

  return (
    <div className="relative" ref={rootRef}>
      <svg
        aria-label="World map"
        className="block w-full text-muted-foreground"
        onPointerLeave={() => setHover(undefined)}
        preserveAspectRatio="xMidYMid meet"
        viewBox={analyticsWorldViewBox}
      >
        <rect
          fill="transparent"
          height="100%"
          onPointerEnter={() => setHover(undefined)}
          onPointerMove={() => setHover(undefined)}
          width="100%"
        />
        {analyticsWorldCountries.map((country) => {
          const visitors = byCode.get(country.code) ?? 0;
          return (
            <path
              className={
                visitors > 0
                  ? "cursor-pointer stroke-background hover:stroke-foreground"
                  : "cursor-default stroke-background/80"
              }
              d={country.d}
              fill="currentColor"
              fillOpacity={
                visitors === 0 ? 0.12 : 0.28 + (visitors / maximum) * 0.72
              }
              key={country.code}
              onClick={() => onSelect(country.code)}
              onPointerEnter={(event) =>
                moveHover(event, country.code, visitors)
              }
              onPointerMove={(event) =>
                moveHover(event, country.code, visitors)
              }
              strokeWidth="0.4"
            />
          );
        })}
      </svg>
      {hover ? (
        <div
          className="pointer-events-none absolute z-10 w-44 border border-border bg-card p-2.5 shadow-lg"
          style={{
            left: Math.max(8, Math.min(hover.x + 12, hover.width - 184)),
            top: Math.max(8, hover.y - 52),
          }}
        >
          <p className="text-[10px] text-foreground">
            {analyticsCountryName(hover.code)}
          </p>
          <p className="mt-1 text-[10px] text-muted-foreground">
            {formatCount(hover.visitors)} visitors
          </p>
        </div>
      ) : null}
    </div>
  );
};
