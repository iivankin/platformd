import type {
  App,
  Issue,
  ReplayRecording,
  StoredDocument,
} from "../web/errors/types";

export interface ErrorsMockState {
  artifacts: StoredDocument[];
  events: StoredDocument[];
  issues: Issue[];
  replayItems: StoredDocument[];
  replayRecording: ReplayRecording;
  service: App;
}

const serviceId = "k9n2p4r7t5v8x3z6b1d4f7h2";
const issueId =
  "92e1e94049fc11f69e4ac5afd4060dabdea164c4738142e27294bc0437e0b097";
const replayId = "82818281828142818281828182818281";

const event = ({
  eventId,
  receivedAt,
  replay = false,
  title,
}: {
  eventId: string;
  receivedAt: string;
  replay?: boolean;
  title: string;
}): StoredDocument => ({
  content_id: `content_${eventId}`,
  doc_kind: "event",
  environment: "production",
  event_id: eventId,
  issue_id: issueId,
  level: "error",
  payload: {
    breadcrumbs: {
      values: [
        {
          category: "navigation",
          data: { from: "/cart", to: "/checkout" },
          level: "info",
          timestamp: "2026-08-09T10:44:38Z",
          type: "navigation",
        },
        {
          category: "ui.click",
          data: { component: "CheckoutButton" },
          level: "info",
          message: "button[data-action=checkout]",
          timestamp: "2026-08-09T10:44:52Z",
          type: "user",
        },
        {
          category: "fetch",
          data: {
            method: "POST",
            status_code: 409,
            url: "/api/inventory/reserve",
          },
          level: "warning",
          message: "Inventory reservation expired",
          timestamp: "2026-08-09T10:45:03Z",
          type: "http",
        },
        {
          category: "console",
          data: { arguments: ["reservation expired", "sku_042"] },
          level: "error",
          message: "Checkout could not be finalized",
          timestamp: "2026-08-09T10:45:04Z",
          type: "default",
        },
      ],
    },
    contexts: {
      browser: { name: "Chrome", type: "browser", version: "140.0.0" },
      device: { brand: "Apple", family: "Mac", model: "MacBookPro18,3" },
      os: { name: "macOS", type: "os", version: "15.6" },
      runtime: { name: "Bun", type: "runtime", version: "1.2.20" },
      trace: {
        op: "ui.action.checkout",
        span_id: "8f3a0f34b17c9d20",
        trace_id: "4c79f60c11214eb38604f4ae0781bfb2",
      },
    },
    environment: "production",
    event_id: eventId,
    exception: {
      values: [
        {
          stacktrace: {
            frames: [
              {
                colno: 11,
                context_line:
                  "throw new CheckoutInvariantError('Inventory reservation expired');",
                filename: "webpack:///src/checkout/cart.ts",
                function: "reserveInventory",
                in_app: true,
                lineno: 71,
                module: "checkout/cart",
                post_context: ["}", "", "export async function checkout() {"],
                pre_context: [
                  "if (!reservation.active) {",
                  "  inventory.release(reservation.id);",
                ],
              },
              {
                colno: 17,
                filename: "webpack:///src/checkout/submit.ts",
                function: "finalizeOrder",
                in_app: true,
                lineno: 184,
                module: "checkout/submit",
              },
              {
                colno: 21,
                filename: "https://shop.example.com/chunk-runtime.js",
                function: "dispatch",
                in_app: false,
                lineno: 1,
                module: "runtime",
              },
            ],
          },
          type: "CheckoutInvariantError",
          value: "Cannot finalize order after inventory reservation expired",
        },
      ],
    },
    extra: {
      cart_id: "cart_9481",
      cart_total: 189.9,
      inventory_reservation: "res_7304",
    },
    level: "error",
    measurements: {
      cls: { unit: "none", value: 0.14 },
      fcp: { unit: "millisecond", value: 820 },
      inp: { unit: "millisecond", value: 168 },
      lcp: { unit: "millisecond", value: 1842 },
      ttfb: { unit: "millisecond", value: 112 },
    },
    platform: "javascript",
    release: "storefront@2.8.1",
    ...(replay ? { replay_id: replayId } : {}),
    request: {
      headers: {
        Referer: "https://shop.example.com/cart",
        "User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)",
      },
      method: "POST",
      query_string: "step=payment",
      url: "https://shop.example.com/checkout/confirm",
    },
    tags: {
      "checkout.flow": "express",
      region: "eu-central-1",
      ...(replay ? { replayId } : {}),
    },
    transaction: "/checkout/confirm",
    user: {
      email: "alex@example.com",
      geo: { city: "Belgrade", country_code: "RS", region: "Belgrade" },
      id: "customer_1042",
      ip_address: "203.0.113.42",
      username: "alex",
    },
  },
  platform: "javascript",
  received_at: receivedAt,
  release: "storefront@2.8.1",
  ...(replay ? { replay_id: replayId } : {}),
  sdk_name: "sentry.javascript.browser",
  sdk_version: "9.42.0",
  service_id: serviceId,
  timestamp: receivedAt,
  title,
  transaction: "/checkout/confirm",
});

const events = [
  event({
    eventId: "abababababababababababababababab",
    receivedAt: "2026-08-09T10:45:04Z",
    replay: true,
    title:
      "CheckoutInvariantError: Cannot finalize order after inventory reservation expired",
  }),
  event({
    eventId: "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd",
    receivedAt: "2026-08-09T10:42:18Z",
    title:
      "CheckoutInvariantError: Cannot finalize order after inventory reservation expired",
  }),
  {
    doc_kind: "event",
    environment: "production",
    event_id: "efefefefefefefefefefefefefefefef",
    issue_id: "issue_payment_timeout",
    level: "error",
    payload: {
      message: "Stripe intent confirmation exceeded 10000ms",
      transaction: "/checkout/confirm",
    },
    platform: "javascript",
    received_at: "2026-08-09T10:39:51Z",
    sdk_name: "sentry.javascript.browser",
    sdk_version: "9.42.0",
    service_id: serviceId,
    timestamp: "2026-08-09T10:39:51Z",
    title: "PaymentGatewayTimeout: Stripe intent confirmation exceeded 10000ms",
  },
] satisfies StoredDocument[];

const replayStartedAt = Date.parse("2026-08-09T10:44:30Z");
const replayRecording: ReplayRecording = {
  errorEvents: [
    {
      doc_kind: "event",
      environment: "production",
      event_id: "abababababababababababababababab",
      issue_id: issueId,
      level: "error",
      platform: "javascript",
      received_at: "2026-08-09T10:45:04Z",
      release: "storefront@2.8.1",
      replay_id: replayId,
      service_id: serviceId,
      timestamp: "2026-08-09T10:45:04Z",
      title:
        "CheckoutInvariantError: Cannot finalize order after inventory reservation expired",
      transaction: "/checkout/confirm",
    },
  ],
  events: [
    {
      data: {
        height: 720,
        href: "https://shop.example.com/checkout",
        width: 1280,
      },
      timestamp: replayStartedAt,
      type: 4,
    },
    {
      data: {
        initialOffset: { left: 0, top: 0 },
        node: {
          childNodes: [
            {
              id: 2,
              name: "html",
              publicId: "",
              systemId: "",
              type: 1,
            },
            {
              attributes: { lang: "en" },
              childNodes: [
                {
                  attributes: {},
                  childNodes: [
                    {
                      attributes: {},
                      childNodes: [
                        {
                          id: 6,
                          textContent: "Checkout · Northstar Supply",
                          type: 3,
                        },
                      ],
                      id: 5,
                      tagName: "title",
                      type: 2,
                    },
                  ],
                  id: 4,
                  tagName: "head",
                  type: 2,
                },
                {
                  attributes: {
                    style:
                      "margin:0;background:#f7f6f2;color:#20201e;font-family:ui-sans-serif,system-ui;",
                  },
                  childNodes: [
                    {
                      attributes: {
                        style:
                          "height:64px;display:flex;align-items:center;justify-content:space-between;padding:0 64px;border-bottom:1px solid #d9d7d0;background:#fff;",
                      },
                      childNodes: [
                        {
                          attributes: {
                            style: "font-weight:700;letter-spacing:.08em;",
                          },
                          childNodes: [
                            { id: 10, textContent: "NORTHSTAR", type: 3 },
                          ],
                          id: 9,
                          tagName: "span",
                          type: 2,
                        },
                        {
                          attributes: {
                            style: "font-size:13px;color:#686761;",
                          },
                          childNodes: [
                            { id: 12, textContent: "Secure checkout", type: 3 },
                          ],
                          id: 11,
                          tagName: "span",
                          type: 2,
                        },
                      ],
                      id: 8,
                      tagName: "header",
                      type: 2,
                    },
                    {
                      attributes: {
                        style:
                          "width:760px;margin:56px auto;display:grid;grid-template-columns:1fr 260px;gap:48px;",
                      },
                      childNodes: [
                        {
                          attributes: {},
                          childNodes: [
                            {
                              attributes: {
                                style: "font-size:28px;margin:0 0 8px;",
                              },
                              childNodes: [
                                {
                                  id: 16,
                                  textContent: "Complete your order",
                                  type: 3,
                                },
                              ],
                              id: 15,
                              tagName: "h1",
                              type: 2,
                            },
                            {
                              attributes: {
                                style: "color:#686761;margin:0 0 32px;",
                              },
                              childNodes: [
                                {
                                  id: 18,
                                  textContent: "Delivery and payment details",
                                  type: 3,
                                },
                              ],
                              id: 17,
                              tagName: "p",
                              type: 2,
                            },
                            {
                              attributes: {
                                placeholder: "Email address",
                                style:
                                  "box-sizing:border-box;width:100%;height:48px;border:1px solid #aaa8a0;padding:0 14px;font-size:14px;background:#fff;",
                                type: "email",
                                value: "alex@example.com",
                              },
                              childNodes: [],
                              id: 19,
                              tagName: "input",
                              type: 2,
                            },
                            {
                              attributes: {
                                style:
                                  "margin-top:16px;padding:13px;border:1px solid #d98b7b;background:#fff3f0;color:#9b2c1c;font-size:13px;",
                              },
                              childNodes: [
                                {
                                  id: 21,
                                  textContent:
                                    "Your inventory reservation is about to expire.",
                                  type: 3,
                                },
                              ],
                              id: 20,
                              tagName: "div",
                              type: 2,
                            },
                            {
                              attributes: {
                                "data-action": "checkout",
                                style:
                                  "width:100%;height:48px;margin-top:16px;border:0;background:#20201e;color:#fff;font-weight:700;cursor:pointer;",
                              },
                              childNodes: [
                                {
                                  id: 23,
                                  textContent: "Place order · $189.90",
                                  type: 3,
                                },
                              ],
                              id: 22,
                              tagName: "button",
                              type: 2,
                            },
                          ],
                          id: 14,
                          tagName: "section",
                          type: 2,
                        },
                        {
                          attributes: {
                            style:
                              "border-left:1px solid #d9d7d0;padding-left:32px;font-size:13px;",
                          },
                          childNodes: [
                            {
                              attributes: { style: "margin:0 0 24px;" },
                              childNodes: [
                                {
                                  id: 26,
                                  textContent: "Order summary",
                                  type: 3,
                                },
                              ],
                              id: 25,
                              tagName: "h2",
                              type: 2,
                            },
                            {
                              attributes: {
                                style: "color:#686761;line-height:1.8;",
                              },
                              childNodes: [
                                {
                                  id: 28,
                                  textContent:
                                    "Field jacket × 1\nExpress delivery\nTotal $189.90",
                                  type: 3,
                                },
                              ],
                              id: 27,
                              tagName: "p",
                              type: 2,
                            },
                          ],
                          id: 24,
                          tagName: "aside",
                          type: 2,
                        },
                      ],
                      id: 13,
                      tagName: "main",
                      type: 2,
                    },
                  ],
                  id: 7,
                  tagName: "body",
                  type: 2,
                },
              ],
              id: 3,
              tagName: "html",
              type: 2,
            },
          ],
          id: 1,
          type: 0,
        },
      },
      timestamp: replayStartedAt + 10,
      type: 2,
    },
    {
      data: {
        payload: {
          category: "navigation",
          data: { from: "/cart", to: "/checkout" },
          timestamp: (replayStartedAt + 3000) / 1000,
          type: "navigation",
        },
        tag: "breadcrumb",
      },
      timestamp: replayStartedAt + 3000,
      type: 5,
    },
    {
      data: {
        payload: {
          data: {
            method: "GET",
            statusCode: 200,
            url: "/api/catalog/cart_9481",
          },
          description: "GET /api/catalog/cart_9481",
          op: "resource.fetch",
          startTimestamp: (replayStartedAt + 9000) / 1000,
        },
        tag: "performanceSpan",
      },
      timestamp: replayStartedAt + 9000,
      type: 5,
    },
    {
      data: {
        id: 19,
        isChecked: false,
        source: 5,
        text: "alex@example.com",
        userTriggered: true,
      },
      timestamp: replayStartedAt + 16_000,
      type: 3,
    },
    {
      data: {
        payload: {
          category: "ui.click",
          data: { component: "CheckoutButton" },
          message: "button[data-action=checkout]",
          timestamp: (replayStartedAt + 22_000) / 1000,
          type: "user",
        },
        tag: "breadcrumb",
      },
      timestamp: replayStartedAt + 22_000,
      type: 5,
    },
    {
      data: { id: 22, source: 2, type: 2, x: 384, y: 407 },
      timestamp: replayStartedAt + 22_000,
      type: 3,
    },
    {
      data: {
        payload: {
          category: "fetch",
          data: {
            method: "POST",
            status_code: 409,
            url: "/api/inventory/reserve",
          },
          level: "warning",
          message: "Inventory reservation expired",
          timestamp: (replayStartedAt + 29_000) / 1000,
          type: "http",
        },
        tag: "breadcrumb",
      },
      timestamp: replayStartedAt + 29_000,
      type: 5,
    },
    {
      data: {
        payload: {
          category: "console",
          level: "error",
          message: "Checkout could not be finalized",
          timestamp: (replayStartedAt + 33_500) / 1000,
          type: "default",
        },
        tag: "breadcrumb",
      },
      timestamp: replayStartedAt + 33_500,
      type: 5,
    },
    {
      data: {
        payload: {
          data: {
            memory: {
              jsHeapSizeLimit: 4_294_967_296,
              totalJSHeapSize: 71_303_168,
              usedJSHeapSize: 48_234_496,
            },
          },
          op: "memory",
          startTimestamp: (replayStartedAt + 32_000) / 1000,
        },
        tag: "performanceSpan",
      },
      timestamp: replayStartedAt + 32_000,
      type: 5,
    },
  ],
  finishedAt: replayStartedAt + 34_000,
  replayId,
  segmentCount: 2,
  startedAt: replayStartedAt,
  traceIds: ["4c79f60c11214eb38604f4ae0781bfb2"],
};

export const createErrorsMockState = (): ErrorsMockState => ({
  artifacts: [
    {
      checksum: "9e91aeb1bc4c1afde35e8b75ed942a21",
      debug_id: "1b9e7dbf-1a8e-4d1e-8d21-251db6866af6",
      doc_kind: "artifact",
      filename: "checkout.js.map",
      item_type: "source_map",
      received_at: "2026-08-09T09:58:00Z",
      service_id: serviceId,
      size_bytes: 482_312,
      symbol_type: "sourcemap",
      timestamp: "2026-08-09T09:58:00Z",
    },
  ],
  events: structuredClone(events),
  issues: [
    {
      eventCount: 2,
      firstSeen: "2026-08-09T10:42:18Z",
      id: issueId,
      lastEventId: "abababababababababababababababab",
      lastSeen: "2026-08-09T10:45:04Z",
      level: "error",
      platform: "javascript",
      status: "open",
      title:
        "CheckoutInvariantError: Cannot finalize order after inventory reservation expired",
    },
    {
      eventCount: 1,
      firstSeen: "2026-08-09T10:39:51Z",
      id: "issue_payment_timeout",
      lastEventId: "efefefefefefefefefefefefefefefef",
      lastSeen: "2026-08-09T10:39:51Z",
      level: "error",
      platform: "javascript",
      status: "open",
      title:
        "PaymentGatewayTimeout: Stripe intent confirmation exceeded 10000ms",
    },
  ],
  replayItems: [
    {
      content_id: "replay_event_payload",
      doc_kind: "replay_event",
      event_id: replayId,
      payload: {
        error_ids: ["abababababababababababababababab"],
        replay_id: replayId,
        sdk: { name: "sentry.javascript.browser" },
        segment_id: 0,
        urls: ["https://shop.example.com/checkout"],
      },
      received_at: "2026-08-09T10:44:30Z",
      replay_id: replayId,
      segment_id: 0,
      service_id: serviceId,
      timestamp: "2026-08-09T10:44:30Z",
    },
    {
      content_id: "replay_recording_payload",
      doc_kind: "replay_recording",
      event_id: replayId,
      received_at: "2026-08-09T10:44:31Z",
      replay_id: replayId,
      segment_id: 1,
      service_id: serviceId,
      timestamp: "2026-08-09T10:44:31Z",
    },
  ],
  replayRecording: structuredClone(replayRecording),
  service: {
    id: serviceId,
    internalDsn: `http://${serviceId}@errors-storefront-web.storefront.internal:9001/1`,
    name: "Storefront Web",
    publicDsn: `http://${serviceId}@127.0.0.1:3101/1`,
    slug: "storefront-web",
    webhooks: [
      {
        createdAt: Date.parse("2026-08-09T08:01:41Z"),
        enabled: true,
        events: ["issue_created", "issue_regressed", "event_received"],
        id: "webhook_platform_errors",
        updatedAt: Date.parse("2026-08-09T08:01:41Z"),
        url: "https://hooks.example.com/platform-errors",
      },
    ],
  },
});
