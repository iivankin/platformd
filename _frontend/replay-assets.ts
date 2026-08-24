import path from "node:path";

export const replayFrameSourcePath = path.join(
  import.meta.dir,
  "web",
  "replay-frame.html"
);

export const replayPlayerStyleSourcePath = path.join(
  import.meta.dir,
  "node_modules",
  "@sentry",
  "rrweb",
  "dist",
  "style.css"
);
