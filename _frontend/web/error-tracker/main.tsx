import { createRoot } from "react-dom/client";

import { initializeTheme } from "@/use-theme";

import { TrackerApp } from "./tracker-app";

import "./styles.css";

initializeTheme();

const root = document.querySelector("#root");
if (!root) {
  throw new Error("Root element not found");
}

createRoot(root).render(<TrackerApp />);
