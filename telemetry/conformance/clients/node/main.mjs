const testCase = process.env.CONFORMANCE_CASE;
const Sentry = testCase === "browser"
  ? await import("@sentry/browser")
  : await import("@sentry/node");
Sentry.init({ dsn: process.env.SENTRY_DSN });
Sentry.setTag("conformance_case", testCase);

if (testCase === "node" || testCase === "browser") {
  Sentry.captureException(new Error("node conformance"));
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
