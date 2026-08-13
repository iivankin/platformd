import { rm, stat } from "node:fs/promises";
import path from "node:path";

import tailwindcss from "tailwindcss-bun-plugin";

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

  await Promise.all(
    result.outputs.map(async (output) => {
      const { size } = await stat(output.path);
      console.log(
        `${path.relative(process.cwd(), output.path)} ${(size / 1024).toFixed(1)} KiB`
      );
    })
  );
};

await build({
  entrypoint: "web/index.html",
  outdir: path.join(process.cwd(), "..", "internal", "ui", "dist"),
});
