import { ExternalLink } from "lucide-react";

const CLOUDFLARE_GEOIP_URL =
  "https://developers.cloudflare.com/network/ip-geolocation/";

export const SentryCloudflareGeoIpHint = () => (
  <p className="mt-1.5 text-[9px] leading-4 text-muted-foreground">
    User countries require{" "}
    <a
      className="inline-flex items-center gap-1 text-foreground underline underline-offset-4"
      href={CLOUDFLARE_GEOIP_URL}
      rel="noreferrer"
      target="_blank"
    >
      Cloudflare Network → IP Geolocation
      <ExternalLink aria-hidden="true" className="size-2.5" />
    </a>{" "}
    enabled for this zone.
  </p>
);
