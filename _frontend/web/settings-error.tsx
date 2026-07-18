import { X } from "lucide-react";

import { SectionCard } from "@/components/ui/card";

export const SettingsError = ({ message }: { message?: string }) => {
  if (!message) {
    return null;
  }
  return (
    <SectionCard className="flex items-center gap-2 bg-destructive/5 px-5 py-3 text-xs text-destructive ring-destructive/30">
      <X className="size-3.5" /> {message}
    </SectionCard>
  );
};
