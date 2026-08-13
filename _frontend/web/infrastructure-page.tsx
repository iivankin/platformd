import { Navigate, Route, Routes } from "react-router";

import { AuditPage } from "@/audit-page";
import { InfrastructureCapacityPage } from "@/infrastructure-capacity-page";
import { InfrastructureLogs } from "@/infrastructure-logs";
import { PageTabs } from "@/page-tabs";
import { InstallationUsage } from "@/resource-usage";

const tabs = [
  { label: "Usage", path: "/monitoring/usage" },
  { label: "Capacity", path: "/monitoring/capacity" },
  { label: "Logs", path: "/monitoring/logs" },
  { label: "Audit", path: "/monitoring/audit" },
];

export const InfrastructurePage = () => (
  <div className="min-h-full animate-in duration-200 fade-in slide-in-from-bottom-1">
    <PageTabs label="Monitoring pages" tabs={tabs} />
    <Routes>
      <Route element={<Navigate replace to="usage" />} index />
      <Route element={<InstallationUsage />} path="usage" />
      <Route element={<InfrastructureCapacityPage />} path="capacity" />
      <Route element={<InfrastructureLogs />} path="logs" />
      <Route element={<AuditPage />} path="audit" />
      <Route element={<Navigate replace to="usage" />} path="*" />
    </Routes>
  </div>
);
