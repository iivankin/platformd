import { Navigate, Route, Routes } from "react-router";

import { PageTabs } from "@/page-tabs";
import { SettingsCertificatesPage } from "@/settings-certificates-page";
import { SettingsGeneralPage } from "@/settings-general-page";

const tabs = [
  { label: "General", path: "/settings/general" },
  { label: "Certificates", path: "/settings/certificates" },
];

export const SettingsPage = () => (
  <div className="enter-row min-h-full">
    <PageTabs label="Settings pages" tabs={tabs} />
    <Routes>
      <Route element={<Navigate replace to="general" />} index />
      <Route element={<SettingsGeneralPage />} path="general" />
      <Route element={<SettingsCertificatesPage />} path="certificates" />
      <Route element={<Navigate replace to="general" />} path="*" />
    </Routes>
  </div>
);
