import { Combobox } from "@base-ui/react/combobox";
import { ShieldCheck } from "lucide-react";
import { useEffect, useState } from "react";

import { fetchInstallationSettings } from "@/api";
import {
  certificateHostnameSuggestionMatches,
  certificateHostnameSuggestions,
  completeCertificateHostname,
} from "@/certificate-hostname-model";
import type { CertificateHostnameSuggestion } from "@/certificate-hostname-model";

export const CertificateHostnameCombobox = ({
  ariaLabel = "Public hostname",
  disabled,
  id,
  onChange,
  placeholder = "api.example.com",
  value,
}: {
  ariaLabel?: string;
  disabled?: boolean;
  id?: string;
  onChange: (value: string) => void;
  placeholder?: string;
  value: string;
}) => {
  const [suggestions, setSuggestions] = useState<
    CertificateHostnameSuggestion[]
  >([]);

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const settings = await fetchInstallationSettings(controller.signal);
        setSuggestions(certificateHostnameSuggestions(settings.certificates));
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setSuggestions([]);
        }
      }
    };
    void load();
    return () => controller.abort();
  }, []);

  return (
    <Combobox.Root<CertificateHostnameSuggestion>
      autoHighlight
      disabled={disabled}
      filter={(suggestion, query) =>
        certificateHostnameSuggestionMatches(suggestion, query)
      }
      inputValue={value}
      itemToStringLabel={(suggestion) => suggestion.dnsName}
      items={suggestions}
      onInputValueChange={(hostname, eventDetails) => {
        if (eventDetails.reason === "input-change") {
          onChange(hostname);
        }
      }}
      onValueChange={(suggestion) => {
        if (suggestion) {
          onChange(completeCertificateHostname(suggestion, value));
        }
      }}
      value={null}
    >
      <Combobox.Input
        aria-label={ariaLabel}
        autoCapitalize="none"
        autoComplete="off"
        className="h-8 w-full border border-input bg-background px-2.5 text-xs text-foreground outline-none placeholder:text-muted-foreground/55 focus-visible:border-foreground/40 focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50"
        id={id}
        placeholder={placeholder}
        spellCheck={false}
      />
      <Combobox.Portal>
        <Combobox.Positioner align="start" className="z-50" sideOffset={4}>
          <Combobox.Popup className="max-h-64 w-[var(--anchor-width)] min-w-72 overflow-y-auto border border-border bg-popover p-1 text-popover-foreground shadow-lg">
            <Combobox.Empty className="px-3 py-4 text-[10px] text-muted-foreground empty:hidden">
              No certificate hostname matches.
            </Combobox.Empty>
            <Combobox.List>
              {(suggestion: CertificateHostnameSuggestion) => {
                const hostname = completeCertificateHostname(suggestion, value);
                return (
                  <Combobox.Item
                    className="grid cursor-default grid-cols-[minmax(0,1fr)_auto] items-center gap-4 px-2.5 py-2 text-[10px] outline-none data-[highlighted]:bg-muted"
                    key={suggestion.dnsName}
                    value={suggestion}
                  >
                    <span className="min-w-0">
                      <span className="block truncate font-mono">
                        {hostname}
                      </span>
                      {suggestion.wildcard ? (
                        <span className="mt-0.5 block truncate text-[8px] text-muted-foreground">
                          Covered by {suggestion.dnsName}
                        </span>
                      ) : null}
                    </span>
                    <span className="flex items-center gap-1 text-[8px] text-muted-foreground uppercase">
                      <ShieldCheck className="size-3" /> Certificate
                    </span>
                  </Combobox.Item>
                );
              }}
            </Combobox.List>
          </Combobox.Popup>
        </Combobox.Positioner>
      </Combobox.Portal>
    </Combobox.Root>
  );
};
