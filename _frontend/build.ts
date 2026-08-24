import { copyFile, readFile, rm, stat, writeFile } from "node:fs/promises";
import path from "node:path";

import tailwindcss from "tailwindcss-bun-plugin";

import {
  replayFrameSourcePath,
  replayPlayerStyleSourcePath,
} from "./replay-assets";

const build = async ({
  entrypoint,
  outdir,
}: {
  entrypoint: string;
  outdir: string;
}) => {
  await rm(outdir, { force: true, recursive: true });
  const result = await Bun.build({
    define: {
      "process.env.NODE_ENV": JSON.stringify("production"),
    },
    entrypoints: [entrypoint],
    minify: true,
    outdir,
    plugins: [tailwindcss],
    publicPath: "/",
    splitting: true,
    target: "browser",
  });

  if (!result.success) {
    for (const log of result.logs) {
      console.error(log);
    }
    process.exit(1);
  }

  // Bun's HTML bundler can inject the wrong split chunk into index.html (e.g. a
  // Shiki grammar) while the real app entry-point is a different hashed file.
  // Always rewrite the module script to the entry-point JS Bun reported.
  const htmlOutput = result.outputs.find(
    (output) => output.kind === "entry-point" && output.path.endsWith(".html")
  );
  const jsEntry = result.outputs.find(
    (output) => output.kind === "entry-point" && output.path.endsWith(".js")
  );
  if (!htmlOutput || !jsEntry) {
    console.error(
      "expected HTML and JS entry-point outputs from Bun.build",
      result.outputs.map((output) => ({ kind: output.kind, path: output.path }))
    );
    process.exit(1);
  }

  const entrySrc = `/${path.basename(jsEntry.path)}`;
  const html = await readFile(htmlOutput.path, "utf-8");
  const rewritten = html.replace(
    /(?<prefix><script type="module"[^>]*\bsrc=")(?<src>[^"]+)(?<suffix>")/u,
    `$<prefix>${entrySrc}$<suffix>`
  );
  if (rewritten === html && !html.includes(`src="${entrySrc}"`)) {
    console.error(
      `failed to point index.html at entry ${entrySrc}; html was:\n${html}`
    );
    process.exit(1);
  }
  if (!rewritten.includes(`src="${entrySrc}"`)) {
    console.error(`index.html is missing entry script ${entrySrc}`);
    process.exit(1);
  }
  const entrySource = await readFile(jsEntry.path, "utf-8");
  if (!entrySource.includes("missing #root mount point")) {
    console.error(
      `entry ${entrySrc} does not look like the app bootstrap (missing root guard)`
    );
    process.exit(1);
  }
  await writeFile(htmlOutput.path, rewritten);
  await copyFile(replayFrameSourcePath, path.join(outdir, "replay-frame.html"));
  await copyFile(
    replayPlayerStyleSourcePath,
    path.join(outdir, "replay-player.css")
  );

  await Promise.all(
    result.outputs.map(async (output) => {
      const { size } = await stat(output.path);
      console.log(
        `${path.relative(process.cwd(), output.path)} ${(size / 1024).toFixed(1)} KiB`
      );
    })
  );
  console.log(`index.html entry -> ${entrySrc}`);
};

await build({
  entrypoint: "web/index.html",
  outdir: path.join(process.cwd(), "..", "internal", "ui", "dist"),
});
