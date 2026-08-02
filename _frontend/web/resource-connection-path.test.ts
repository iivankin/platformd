import { expect, test } from "bun:test";

import { resourceConnectionPoints } from "@/resource-connection-path";

test("keeps an ELK route orthogonal while its nodes are dragged", () => {
  expect(
    resourceConnectionPoints(
      [
        { x: 328, y: 94 },
        { x: 348, y: 94 },
        { x: 348, y: 266 },
        { x: 472, y: 266 },
      ],
      { x: 400, y: 160 },
      { x: 720, y: 300 }
    )
  ).toEqual([
    { x: 400, y: 160 },
    { x: 444.44444444444446, y: 160 },
    { x: 444.44444444444446, y: 300 },
    { x: 720, y: 300 },
  ]);
});

test("adds a Manhattan channel when a straight edge is dragged off-axis", () => {
  expect(
    resourceConnectionPoints(
      [
        { x: 328, y: 114 },
        { x: 472, y: 114 },
      ],
      { x: 360, y: 140 },
      { x: 600, y: 260 }
    )
  ).toEqual([
    { x: 360, y: 140 },
    { x: 480, y: 140 },
    { x: 480, y: 260 },
    { x: 600, y: 260 },
  ]);
});
