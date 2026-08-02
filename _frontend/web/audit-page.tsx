import { AuditEventsView } from "@/audit-events-view";
import { PageStack } from "@/components/ui/page-stack";

export const AuditPage = () => (
  <PageStack className="animate-in duration-200 fade-in slide-in-from-bottom-1">
    <AuditEventsView />
  </PageStack>
);
