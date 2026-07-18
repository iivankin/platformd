import { Navigate, Route, Routes } from "react-router";

import { PageTabs } from "@/page-tabs";
import { SettingsCertificatesPage } from "@/settings-certificates-page";
import { SettingsGeneralPage } from "@/settings-general-page";

const tabs = [
  { label: "General", path: "/settings/general" },
  { label: "Certificates", path: "/settings/certificates" },
];

export const SettingsPage = () => (
  <div className="min-h-full animate-in duration-200 fade-in slide-in-from-bottom-1">
    <PageTabs label="Settings pages" tabs={tabs} />
    <Routes>
      <Route element={<Navigate replace to="general" />} index />
      <Route element={<SettingsGeneralPage />} path="general" />
      <Route element={<SettingsCertificatesPage />} path="certificates" />
      <Route element={<Navigate replace to="general" />} path="*" />
    </Routes>
  </div>
);
