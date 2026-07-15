import { X } from "lucide-react";

export const SettingsError = ({ message }: { message?: string }) => {
  if (!message) {
    return null;
  }
  return (
    <section className="flex items-center gap-2 border-b border-destructive/30 bg-destructive/5 px-5 py-3 text-xs text-destructive">
      <X className="size-3.5" /> {message}
    </section>
  );
};
