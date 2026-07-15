import { Navigate, Route, Routes } from "react-router";

import { InfrastructureCapacityPage } from "@/infrastructure-capacity-page";
import { InfrastructureLogs } from "@/infrastructure-logs";
import { InfrastructureOperationsPage } from "@/infrastructure-operations-page";
import { PageTabs } from "@/page-tabs";

const tabs = [
  { label: "Capacity", path: "/infrastructure/capacity" },
  { label: "Operations", path: "/infrastructure/operations" },
  { label: "Logs", path: "/infrastructure/logs" },
];

export const InfrastructurePage = () => (
  <div className="enter-row min-h-full">
    <PageTabs label="Infrastructure pages" tabs={tabs} />
    <Routes>
      <Route element={<Navigate replace to="capacity" />} index />
      <Route element={<InfrastructureCapacityPage />} path="capacity" />
      <Route element={<InfrastructureOperationsPage />} path="operations" />
      <Route element={<InfrastructureLogs />} path="logs" />
      <Route element={<Navigate replace to="capacity" />} path="*" />
    </Routes>
  </div>
);
