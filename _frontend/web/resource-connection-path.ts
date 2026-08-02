import type { XYPosition } from "@xyflow/react";

const samePoint = (left: XYPosition, right: XYPosition): boolean =>
  left.x === right.x && left.y === right.y;

const removeRedundantPoints = (points: XYPosition[]): XYPosition[] => {
  const result: XYPosition[] = [];
  for (const point of points) {
    const previous = result.at(-1);
    if (previous && samePoint(previous, point)) {
      continue;
    }
    const beforePrevious = result.at(-2);
    if (
      previous &&
      beforePrevious &&
      ((beforePrevious.x === previous.x && previous.x === point.x) ||
        (beforePrevious.y === previous.y && previous.y === point.y))
    ) {
      result[result.length - 1] = point;
      continue;
    }
    result.push(point);
  }
  return result;
};

export const resourceConnectionPoints = (
  layoutPoints: XYPosition[],
  source: XYPosition,
  target: XYPosition
): XYPosition[] => {
  if (layoutPoints.length < 3) {
    if (source.y === target.y) {
      return [source, target];
    }
    const middleX = source.x + (target.x - source.x) / 2;
    return [
      source,
      { x: middleX, y: source.y },
      { x: middleX, y: target.y },
      target,
    ];
  }

  const layoutSourceX = layoutPoints[0]?.x ?? source.x;
  const layoutTargetX = layoutPoints.at(-1)?.x ?? target.x;
  const layoutWidth = layoutTargetX - layoutSourceX;
  // Scale ELK's channel positions into the live node gap so dragging stays
  // orthogonal without recalculating the whole graph on every pointer move.
  const points = layoutPoints.map((point) => ({
    ...point,
    x:
      layoutWidth === 0
        ? source.x
        : source.x +
          ((point.x - layoutSourceX) / layoutWidth) * (target.x - source.x),
  }));
  points[0] = source;
  points[1] = { x: points[1]?.x ?? source.x, y: source.y };
  points[points.length - 2] = {
    x: points.at(-2)?.x ?? target.x,
    y: target.y,
  };
  points[points.length - 1] = target;
  return removeRedundantPoints(points);
};
