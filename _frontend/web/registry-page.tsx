import { Navigate, Outlet, Route, Routes } from "react-router";

import { PageTabs } from "@/page-tabs";
import { RegistryImagePage } from "@/registry-image-page";
import { RegistryRepositoriesPage } from "@/registry-repositories-page";
import { RegistryRepositoryPage } from "@/registry-repository-page";
import { RegistrySettingsPage } from "@/registry-settings-page";

const tabs = [
  { label: "Repositories", path: "/registry/repositories" },
  { label: "Settings", path: "/registry/settings" },
];

const RegistrySection = () => (
  <div className="flex min-h-full animate-in flex-col duration-200 fade-in slide-in-from-bottom-1">
    <PageTabs label="Registry pages" tabs={tabs} />
    <Outlet />
  </div>
);

export const RegistryPage = () => (
  <Routes>
    <Route element={<Navigate replace to="repositories" />} index />
    <Route element={<RegistrySection />}>
      <Route element={<RegistryRepositoriesPage />} path="repositories" />
      <Route element={<RegistrySettingsPage />} path="settings" />
    </Route>
    <Route
      element={<RegistryImagePage />}
      path="repositories/:repositoryID/images/:imageDigest"
    />
    <Route
      element={<RegistryRepositoryPage />}
      path="repositories/:repositoryID/:view?"
    />
    <Route element={<Navigate replace to="repositories" />} path="*" />
  </Routes>
);
