# @platformd/analytics

Typed browser analytics for applications hosted by platformd.

```ts
import { createAnalytics } from "@platformd/analytics";

type Events = {
  checkout_completed: {
    currency: string;
    orderId: string;
    revenue: number;
  };
};

export const analytics = createAnalytics<Events>({
  cookieDomain: ".example.com",
  mode: "opt-out",
});

analytics.track("checkout_completed", {
  currency: "USD",
  orderId: "order-1",
  revenue: 49.99,
});
```

Initialize the client once in the browser entry module. It automatically records page views, SPA navigation, page leave, click heatmaps, and scroll heatmaps.

Choose the mode in application code: `cookieless` sends without identity cookies, `opt-in` waits for `analytics.consent("granted")`, and `opt-out` starts immediately. Calling `analytics.consent("denied")` removes the anonymous and session cookies and stops delivery until consent is granted. Global Privacy Control automatically forces cookieless delivery.

The ingest endpoint does not store a tracker-wide mode. It drops requests with denied consent, treats requests with `platformd_aid` as identified, and assigns a rotating daily hash when that cookie is absent or GPC is enabled.
