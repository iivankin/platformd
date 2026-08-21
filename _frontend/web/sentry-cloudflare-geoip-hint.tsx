import { ExternalLink } from "lucide-react";

const CLOUDFLARE_GEOIP_URL =
  "https://developers.cloudflare.com/rules/transform/managed-transforms/reference/#add-visitor-location-headers";

export const SentryCloudflareGeoIpHint = () => (
  <p className="mt-1.5 text-[9px] leading-4 text-muted-foreground">
    User city and region require Cloudflare Rules → Settings → Managed
    Transforms →{" "}
    <a
      className="inline-flex items-center gap-1 text-foreground underline underline-offset-4"
      href={CLOUDFLARE_GEOIP_URL}
      rel="noreferrer"
      target="_blank"
    >
      Add visitor location headers
      <ExternalLink aria-hidden="true" className="size-2.5" />
    </a>
    . Without it, only the country may be available.
  </p>
);
