using Sentry;

var testCase = Environment.GetEnvironmentVariable("CONFORMANCE_CASE")
    ?? throw new InvalidOperationException("CONFORMANCE_CASE is required");
var dsn = Environment.GetEnvironmentVariable("SENTRY_DSN")
    ?? throw new InvalidOperationException("SENTRY_DSN is required");

using var sentry = SentrySdk.Init(options => options.Dsn = dsn);
SentrySdk.ConfigureScope(scope => scope.SetTag("conformance_case", testCase));
SentrySdk.CaptureException(new InvalidOperationException("dotnet conformance"));
await SentrySdk.FlushAsync(TimeSpan.FromSeconds(10));
