import { mkdtemp, rm } from "node:fs/promises";
import path from "node:path";

import { browserTelemetrySetup } from "../web/browser-otel-setup";

const [configuredEndpoint, configuredOutput] = Bun.argv.slice(2);
const endpoint = configuredEndpoint ?? "http://127.0.0.1/otel";
const temporaryDirectory = await mkdtemp(
  path.join(import.meta.dir, ".browser-otel-")
);
const outputPath = configuredOutput
  ? path.resolve(configuredOutput)
  : path.join(temporaryDirectory, "telemetry.js");
const entrypoint = path.join(temporaryDirectory, "telemetry.ts");

const conformanceSource = `${browserTelemetrySetup({
  conformance: true,
  endpoint,
  sentryDsn: "https://public@example.invalid/1",
  serviceName: "platformd-browser-conformance",
})}

const conformanceLogger = logs.getLogger("platformd-browser-conformance");
const windowLoaded =
  document.readyState === "complete"
    ? Promise.resolve()
    : new Promise<void>((resolve) => {
        window.addEventListener("load", () => resolve(), { once: true });
      });
void traced("browser conformance", async () => {
  conformanceLogger.emit({ body: "browser telemetry conformance" });
}).then(async () => {
  await Promise.all([webVitalEmitted, windowLoaded]);
  // Let document-load listeners finish before flushing its correlated span.
  await new Promise((resolve) => setTimeout(resolve, 0));
  await Promise.all([provider.forceFlush(), loggerProvider.forceFlush()]);
  document.body.dataset.telemetryStatus = "exported";
}).catch((error: unknown) => {
  document.body.dataset.telemetryError = String(error);
});`;

try {
  await Bun.write(entrypoint, conformanceSource);
  const result = await Bun.build({
    entrypoints: [entrypoint],
    naming: path.basename(outputPath),
    outdir: path.dirname(outputPath),
    sourcemap: "none",
    target: "browser",
  });
  if (!result.success) {
    throw new AggregateError(
      result.logs,
      "generated browser telemetry setup failed to build"
    );
  }
  if (!configuredOutput) {
    const [output] = result.outputs;
    if (!(output && output.size > 0)) {
      throw new Error("generated browser telemetry bundle is empty");
    }
  }
} finally {
  await rm(temporaryDirectory, { force: true, recursive: true });
}
