const testCase = process.env.CONFORMANCE_CASE;
const Sentry = testCase === "browser"
  ? await import("@sentry/browser")
  : await import("@sentry/node");
const options = { dsn: process.env.SENTRY_DSN };
if (testCase === "node-profile") {
  const { nodeProfilingIntegration } = await import("@sentry/profiling-node");
  options.integrations = [nodeProfilingIntegration()];
  options.tracesSampleRate = 1;
  options.profileSessionSampleRate = 1;
  options.profileLifecycle = "manual";
}
Sentry.init(options);
Sentry.setTag("conformance_case", testCase);

if (testCase === "node" || testCase === "browser") {
  Sentry.captureException(new Error("node conformance"));
} else if (testCase === "node-profile") {
  Sentry.profiler.startProfiler();
  for (const sequence of [1, 2]) {
    await Sentry.startSpan(
      { name: `node profile conformance ${sequence}` },
      async () => {
        Sentry.withScope((scope) => {
          scope.setTag("profile_trace", String(sequence));
          Sentry.captureException(
            new Error(`node profile conformance ${sequence}`)
          );
        });
        const deadline = Date.now() + 750;
        let value = sequence;
        while (Date.now() < deadline) value += Math.sqrt(value + 1);
      }
    );
  }
  Sentry.profiler.stopProfiler();
} else if (testCase === "express") {
  const { default: express } = await import("express");
  const app = express();
  app.get("/fail", () => { throw new Error("express conformance"); });
  Sentry.setupExpressErrorHandler(app);
  const server = app.listen(0);
  const port = server.address().port;
  await fetch(`http://127.0.0.1:${port}/fail`);
  await new Promise((resolve) => server.close(resolve));
} else {
  throw new Error(`unsupported case: ${testCase}`);
}

if (!await Sentry.flush(10_000)) throw new Error("Sentry SDK flush timed out");
