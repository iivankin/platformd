import { Menu } from "@base-ui/react/menu";
import { Check, Monitor, Moon, Sun } from "lucide-react";

import type { Theme } from "@/use-theme";
import { useTheme } from "@/use-theme";

const themeOptions = [
  { icon: Sun, label: "Light", value: "light" as const },
  { icon: Moon, label: "Dark", value: "dark" as const },
  { icon: Monitor, label: "System", value: "system" as const },
];

const isTheme = (value: unknown): value is Theme =>
  value === "light" || value === "dark" || value === "system";

export const ThemeMenuItems = () => {
  const { setTheme, theme } = useTheme();

  return (
    <Menu.Group>
      <Menu.GroupLabel className="px-2.5 pt-2 pb-1 text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
        Appearance
      </Menu.GroupLabel>
      <Menu.RadioGroup
        onValueChange={(value) => {
          if (isTheme(value)) {
            setTheme(value);
          }
        }}
        value={theme}
      >
        {themeOptions.map(({ icon: Icon, label, value }) => (
          <Menu.RadioItem
            className="flex cursor-default items-center gap-2 px-2.5 py-2 text-[10px] text-muted-foreground outline-none data-[checked]:text-foreground data-[highlighted]:bg-muted data-[highlighted]:text-foreground"
            closeOnClick
            key={value}
            value={value}
          >
            <Icon className="size-3.5" />
            {label}
            <Menu.RadioItemIndicator className="ml-auto">
              <Check className="size-3" />
            </Menu.RadioItemIndicator>
          </Menu.RadioItem>
        ))}
      </Menu.RadioGroup>
    </Menu.Group>
  );
};
