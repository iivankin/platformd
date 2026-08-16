import { describe, expect, test } from "bun:test";

import type { ServiceTraceSpan } from "@/api";
import {
  traceSemanticContext,
  traceSpanEvents,
  traceSpanLinks,
  traceSpanSelfTime,
} from "@/trace-span-context";

const span = (overrides: Partial<ServiceTraceSpan> = {}): ServiceTraceSpan =>
  ({
    durationNano: "100",
    endTimeUnixNano: "200",
    parentSpanId: "",
    resource: { attributes: [] },
    span: { attributes: [] },
    spanId: "0000000000000001",
    startTimeUnixNano: "100",
    ...overrides,
  }) as ServiceTraceSpan;

describe("trace span context", () => {
  test("promotes standard OTLP HTTP and runtime attributes", () => {
    const groups = traceSemanticContext(
      span({
        resource: {
          attributes: [
            {
              key: "deployment.environment.name",
              value: { stringValue: "production" },
            },
          ],
        },
        span: {
          attributes: [
            {
              key: "http.request.method",
              value: { stringValue: "POST" },
            },
            { key: "http.route", value: { stringValue: "/checkout" } },
          ],
        },
      })
    );
    expect(groups).toEqual([
      {
        label: "HTTP",
        values: [
          { label: "Method", value: "POST" },
          { label: "Route", value: "/checkout" },
        ],
      },
      {
        label: "Runtime",
        values: [{ label: "Environment", value: "production" }],
      },
    ]);
  });

  test("extracts exception events and computes self time without double counting overlapping children", () => {
    const root = span({
      endTimeUnixNano: "300",
      span: {
        events: [
          {
            attributes: [
              {
                key: "exception.message",
                value: { stringValue: "checkout failed" },
              },
            ],
            name: "exception",
            timeUnixNano: "250",
          },
        ],
      },
    });
    const children = [
      span({
        endTimeUnixNano: "220",
        parentSpanId: root.spanId,
        spanId: "0000000000000002",
        startTimeUnixNano: "150",
      }),
      span({
        endTimeUnixNano: "250",
        parentSpanId: root.spanId,
        spanId: "0000000000000003",
        startTimeUnixNano: "200",
      }),
    ];
    expect(traceSpanEvents(root)).toEqual([
      {
        attributes: [{ label: "exception.message", value: "checkout failed" }],
        name: "exception",
        timeUnixNano: "250",
      },
    ]);
    expect(traceSpanSelfTime(root, [root, ...children])).toBe(100n);
  });

  test("reads Sentry v2 typed attributes and snake-case span links", () => {
    const value = span({
      span: {
        attributes: {
          "http.request.method": { type: "string", value: "POST" },
          "http.response.status_code": { type: "integer", value: 202 },
        },
        links: [
          {
            attributes: {
              "sentry.link.type": {
                type: "string",
                value: "previous_trace",
              },
            },
            span_id: "438f40bd3b4a41ee",
            trace_id: "627a2885119dcc8184fae7eef09438cb",
          },
        ],
      },
    });

    expect(traceSemanticContext(value)[0]).toEqual({
      label: "HTTP",
      values: [
        { label: "Method", value: "POST" },
        { label: "Status", value: "202" },
      ],
    });
    expect(traceSpanLinks(value)).toEqual([
      {
        attributes: [{ label: "sentry.link.type", value: "previous_trace" }],
        spanID: "438f40bd3b4a41ee",
        traceID: "627a2885119dcc8184fae7eef09438cb",
      },
    ]);
  });
});
