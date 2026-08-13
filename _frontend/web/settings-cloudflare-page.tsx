import { CloudflareAccessSettings } from "@/cloudflare-access-settings";
import { CloudflareDNSSettings } from "@/cloudflare-dns-settings";
import { PageStack } from "@/components/ui/page-stack";
import { SettingsCertificatesPage } from "@/settings-certificates-page";

export const SettingsCloudflarePage = () => (
  <PageStack>
    <CloudflareAccessSettings />
    <CloudflareDNSSettings />
    <SettingsCertificatesPage />
  </PageStack>
);
