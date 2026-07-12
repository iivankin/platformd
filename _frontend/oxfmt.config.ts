import { defineConfig } from "oxfmt";
import ultracite from "ultracite/oxfmt";

export default defineConfig({
  ...ultracite,
  ignorePatterns: [
    ...(ultracite.ignorePatterns ?? []),
    "internal/ui/dist/**",
    "coverage/**",
  ],
  sortTailwindcss: {
    functions: ["cn", "cva", "clsx"],
    stylesheet: "./web/styles.css",
  },
});
