import { copyFile, mkdir, rm, writeFile } from "node:fs/promises";
import path from "node:path";

const repositoryRoot = path.resolve(import.meta.dir, "..");
const outputDirectory = path.join(repositoryRoot, "dist", "npm", "analytics");
const version = process.env.VERSION || "0.1.0-dev";

await rm(outputDirectory, { force: true, recursive: true });
await mkdir(outputDirectory, { recursive: true });

const build = await Bun.build({
  entrypoints: [path.join(import.meta.dir, "analytics-sdk", "index.ts")],
  format: "esm",
  minify: true,
  naming: "index.js",
  outdir: outputDirectory,
  sourcemap: "external",
  target: "browser",
});
if (!build.success) {
  for (const message of build.logs) {
    console.error(message);
  }
  process.exit(1);
}

const declarations = Bun.spawn(
  ["bunx", "tsc", "-p", "analytics-sdk/tsconfig.build.json"],
  { cwd: import.meta.dir, stderr: "inherit", stdout: "inherit" }
);
if ((await declarations.exited) !== 0) {
  process.exit(1);
}

await copyFile(
  path.join(import.meta.dir, "analytics-sdk", "README.md"),
  path.join(outputDirectory, "README.md")
);
await copyFile(
  path.join(repositoryRoot, "LICENSE"),
  path.join(outputDirectory, "LICENSE")
);
await writeFile(
  path.join(outputDirectory, "package.json"),
  `${JSON.stringify(
    {
      description:
        "Typed browser analytics for applications hosted by platformd",
      exports: {
        // Conditional exports are matched in declaration order; TypeScript's
        // types condition must be checked before the runtime import.
        // oxlint-disable-next-line eslint/sort-keys
        ".": {
          types: "./index.d.ts",
          import: "./index.js",
        },
      },
      license: "Apache-2.0",
      name: "@platformd/analytics",
      publishConfig: { access: "public" },
      repository: {
        directory: "_frontend/analytics-sdk",
        type: "git",
        url: "git+https://github.com/iivankin/platformd.git",
      },
      sideEffects: false,
      type: "module",
      types: "./index.d.ts",
      version,
    },
    null,
    2
  )}\n`
);

console.log(`built @platformd/analytics ${version} in ${outputDirectory}`);
