import { describe, expect, test } from "bun:test";

import { validTelemetryPublicPath } from "./public-telemetry-endpoint";

describe("public telemetry path validation", () => {
  test("matches the control-plane path contract", () => {
    expect(validTelemetryPublicPath("/otel")).toBe(true);
    expect(validTelemetryPublicPath("/телеметрия")).toBe(true);
    expect(validTelemetryPublicPath("/otel/v2")).toBe(true);
    expect(validTelemetryPublicPath("/otel space")).toBe(false);
    expect(validTelemetryPublicPath("/otel%20space")).toBe(false);
    expect(validTelemetryPublicPath("/otel/../admin")).toBe(false);
    expect(validTelemetryPublicPath("/otel/")).toBe(false);
  });
});
