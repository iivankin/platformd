import { Navigate, Route, Routes } from "react-router";

import type { Project } from "@/api";
import { BackupsPage } from "@/backups-page";
import { PageTabs } from "@/page-tabs";
import { SettingsCloudflarePage } from "@/settings-cloudflare-page";
import { SettingsGeneralPage } from "@/settings-general-page";
import { SettingsMCPPage } from "@/settings-mcp-page";
import type { UpdateStatusState } from "@/use-update-status";

const tabs = [
  { label: "General", path: "/settings/general" },
  { label: "Cloudflare", path: "/settings/cloudflare" },
  { label: "Backups", path: "/settings/backups" },
  { label: "MCP & API", path: "/settings/mcp" },
];

export const SettingsPage = ({
  projects,
  update,
}: {
  projects: Project[];
  update: UpdateStatusState;
}) => (
  <div className="min-h-full animate-in duration-200 fade-in slide-in-from-bottom-1">
    <PageTabs label="Settings pages" tabs={tabs} />
    <Routes>
      <Route element={<Navigate replace to="general" />} index />
      <Route element={<SettingsGeneralPage update={update} />} path="general" />
      <Route element={<SettingsCloudflarePage />} path="cloudflare" />
      <Route element={<BackupsPage />} path="backups/*" />
      <Route element={<SettingsMCPPage projects={projects} />} path="mcp" />
      <Route element={<Navigate replace to="general" />} path="*" />
    </Routes>
  </div>
);
