import { Navigate, Route, Routes } from "react-router";

import { AuditPage } from "@/audit-page";
import { InfrastructureCapacityPage } from "@/infrastructure-capacity-page";
import { InfrastructureLogs } from "@/infrastructure-logs";
import { PageTabs } from "@/page-tabs";

const tabs = [
  { label: "Capacity", path: "/system/capacity" },
  { label: "Logs", path: "/system/logs" },
  { label: "Audit", path: "/system/audit" },
];

export const InfrastructurePage = () => (
  <div className="min-h-full animate-in duration-200 fade-in slide-in-from-bottom-1">
    <PageTabs label="System pages" tabs={tabs} />
    <Routes>
      <Route element={<Navigate replace to="capacity" />} index />
      <Route element={<InfrastructureCapacityPage />} path="capacity" />
      <Route element={<InfrastructureLogs />} path="logs" />
      <Route element={<AuditPage />} path="audit" />
      <Route element={<Navigate replace to="capacity" />} path="*" />
    </Routes>
  </div>
);
