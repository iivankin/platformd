import { Navigate, Route, Routes } from "react-router";

import { BackupStorageCreatePage } from "@/backup-storage-create-page";
import { BackupStoragePage } from "@/backup-storage-page";

export const BackupsPage = () => (
  <div className="min-h-full animate-in duration-200 fade-in slide-in-from-bottom-1">
    <Routes>
      <Route element={<Navigate replace to="storage" />} index />
      <Route element={<BackupStoragePage />} path="storage" />
      <Route element={<BackupStorageCreatePage />} path="storage/new" />
      <Route element={<Navigate replace to="storage" />} path="*" />
    </Routes>
  </div>
);
