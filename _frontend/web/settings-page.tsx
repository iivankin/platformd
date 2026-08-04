import { Navigate, Route, Routes } from "react-router";

import type { Project } from "@/api";
import { APITokensPage } from "@/api-tokens-page";
import { BackupsPage } from "@/backups-page";
import { PageTabs } from "@/page-tabs";
import { SettingsCertificatesPage } from "@/settings-certificates-page";
import { SettingsCloudflarePage } from "@/settings-cloudflare-page";
import { SettingsGeneralPage } from "@/settings-general-page";
import { SettingsMCPPage } from "@/settings-mcp-page";

const tabs = [
  { label: "General", path: "/settings/general" },
  { label: "Certificates", path: "/settings/certificates" },
  { label: "Cloudflare", path: "/settings/cloudflare" },
  { label: "Backups", path: "/settings/backups" },
  { label: "MCP & API", path: "/settings/mcp" },
  { label: "API Tokens", path: "/settings/tokens" },
];

export const SettingsPage = ({ projects }: { projects: Project[] }) => (
  <div className="min-h-full animate-in duration-200 fade-in slide-in-from-bottom-1">
    <PageTabs label="Settings pages" tabs={tabs} />
    <Routes>
      <Route element={<Navigate replace to="general" />} index />
      <Route element={<SettingsGeneralPage />} path="general" />
      <Route element={<SettingsCertificatesPage />} path="certificates" />
      <Route element={<SettingsCloudflarePage />} path="cloudflare" />
      <Route element={<BackupsPage />} path="backups/*" />
      <Route element={<SettingsMCPPage projects={projects} />} path="mcp" />
      <Route element={<APITokensPage projects={projects} />} path="tokens" />
      <Route element={<Navigate replace to="general" />} path="*" />
    </Routes>
  </div>
);
