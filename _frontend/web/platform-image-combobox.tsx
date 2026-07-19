import { Combobox } from "@base-ui/react/combobox";
import { Boxes } from "lucide-react";
import { useEffect, useState } from "react";

import { fetchRegistryImages, fetchRegistryRepositories } from "@/api";

interface ImageSuggestion {
  reference: string;
}

export const PlatformImageCombobox = ({
  hostname,
  id,
  onChange,
  value,
}: {
  hostname: string;
  id: string;
  onChange: (value: string) => void;
  value: string;
}) => {
  const [items, setItems] = useState<ImageSuggestion[]>([]);

  useEffect(() => {
    if (!hostname) {
      return;
    }
    const controller = new AbortController();
    const load = async () => {
      try {
        const repositories = await fetchRegistryRepositories(controller.signal);
        const images = await Promise.all(
          repositories.map(async (repository) => {
            const page = await fetchRegistryImages(
              repository.id,
              { limit: 100 },
              controller.signal
            );
            return { images: page.images, repository };
          })
        );
        setItems(
          images.flatMap(({ images: repositoryImages, repository }) =>
            repositoryImages.flatMap((image) =>
              image.tags.map((tag) => ({
                reference: `${hostname}/${repository.name}:${tag}`,
              }))
            )
          )
        );
      } catch (error) {
        if (!(error instanceof DOMException && error.name === "AbortError")) {
          setItems([]);
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [hostname]);

  return (
    <Combobox.Root<ImageSuggestion>
      autoHighlight
      inputValue={value}
      itemToStringLabel={(item) => item.reference}
      items={items}
      onInputValueChange={(next, details) => {
        if (details.reason === "input-change") {
          onChange(next);
        }
      }}
      onValueChange={(item) => item && onChange(item.reference)}
      value={null}
    >
      <Combobox.Input
        autoCapitalize="none"
        autoComplete="off"
        className="h-8 w-full border border-input bg-background px-2.5 text-xs text-foreground outline-none placeholder:text-muted-foreground/55 focus-visible:border-foreground/40 focus-visible:ring-1 focus-visible:ring-ring"
        id={id}
        placeholder={`${hostname || "registry.example.com"}/team/api:latest`}
        spellCheck={false}
      />
      <Combobox.Portal>
        <Combobox.Positioner align="start" className="z-50" sideOffset={4}>
          <Combobox.Popup className="max-h-64 w-[var(--anchor-width)] min-w-80 overflow-y-auto border border-border bg-popover p-1 text-popover-foreground shadow-lg">
            <Combobox.Empty className="px-3 py-4 text-[10px] text-muted-foreground empty:hidden">
              No matching image tags.
            </Combobox.Empty>
            <Combobox.List>
              {(item: ImageSuggestion) => (
                <Combobox.Item
                  className="flex cursor-default items-center gap-2 px-2.5 py-2 text-[10px] outline-none data-[highlighted]:bg-muted"
                  key={item.reference}
                  value={item}
                >
                  <Boxes className="size-3 text-muted-foreground" />
                  <span className="truncate font-mono">{item.reference}</span>
                </Combobox.Item>
              )}
            </Combobox.List>
          </Combobox.Popup>
        </Combobox.Positioner>
      </Combobox.Portal>
    </Combobox.Root>
  );
};
