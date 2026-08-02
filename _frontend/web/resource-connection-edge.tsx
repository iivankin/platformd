import { BaseEdge } from "@xyflow/react";
import type { EdgeProps, XYPosition } from "@xyflow/react";
import { memo } from "react";

import type { ResourceFlowEdge } from "@/project-flow";
import { resourceConnectionPoints } from "@/resource-connection-path";

const svgPath = (points: XYPosition[]): string =>
  points
    .map((point, index) => `${index === 0 ? "M" : "L"} ${point.x} ${point.y}`)
    .join(" ");

const ResourceConnectionEdgeComponent = ({
  data,
  id,
  interactionWidth,
  markerEnd,
  markerStart,
  sourceX,
  sourceY,
  style,
  targetX,
  targetY,
}: EdgeProps<ResourceFlowEdge>) => {
  const points = resourceConnectionPoints(
    data?.points ?? [],
    { x: sourceX, y: sourceY },
    { x: targetX, y: targetY }
  );
  return (
    <BaseEdge
      id={id}
      interactionWidth={interactionWidth}
      markerEnd={markerEnd}
      markerStart={markerStart}
      path={svgPath(points)}
      style={style}
    />
  );
};

export const ResourceConnectionEdge = memo(ResourceConnectionEdgeComponent);
