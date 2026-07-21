import tailwindcss from "tailwindcss-bun-plugin";

const outfile = process.env.MOCK_BINARY_OUTPUT ?? "dist/platformd-ui-mock";

const result = await Bun.build({
  compile: {
    autoloadBunfig: false,
    autoloadDotenv: false,
    outfile,
  },
  define: {
    "process.env.NODE_ENV": JSON.stringify("production"),
  },
  entrypoints: ["dev.ts"],
  minify: true,
  plugins: [tailwindcss],
  target: "bun",
});

if (!result.success) {
  for (const log of result.logs) {
    console.error(log);
  }
  process.exit(1);
}

console.log(`compiled ${outfile}`);
