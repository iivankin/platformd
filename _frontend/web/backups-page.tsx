import { Navigate, Route, Routes } from "react-router";

import { BackupStoragePage } from "@/backup-storage-page";

export const BackupsPage = () => (
  <div className="enter-row min-h-full">
    <Routes>
      <Route element={<Navigate replace to="storage" />} index />
      <Route element={<BackupStoragePage />} path="storage" />
      <Route element={<Navigate replace to="storage" />} path="*" />
    </Routes>
  </div>
);
