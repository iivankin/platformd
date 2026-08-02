import { CloudflareAccessSettings } from "@/cloudflare-access-settings";
import { CloudflareDNSSettings } from "@/cloudflare-dns-settings";
import { PageStack } from "@/components/ui/page-stack";

export const SettingsCloudflarePage = () => (
  <PageStack>
    <CloudflareAccessSettings />
    <CloudflareDNSSettings />
  </PageStack>
);
