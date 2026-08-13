package dev.platformd;

import io.sentry.Sentry;

public final class Conformance {
    private Conformance() {}

    public static void main(String[] args) {
        String testCase = required("CONFORMANCE_CASE");
        Sentry.init(options -> options.setDsn(required("SENTRY_DSN")));
        Sentry.configureScope(scope -> scope.setTag("conformance_case", testCase));
        Sentry.captureException(new IllegalStateException("java conformance"));
        Sentry.flush(10_000);
        Sentry.close();
    }

    private static String required(String name) {
        String value = System.getenv(name);
        if (value == null || value.isBlank()) throw new IllegalArgumentException(name + " is required");
        return value;
    }
}
