import { NuqsAdapter } from "nuqs/adapters/react-router/v7";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router";

import { App } from "@/app";
import { initializeTheme } from "@/use-theme";

import "@/styles.css";

const root = document.querySelector("#root");

if (!root) {
  throw new Error("missing #root mount point");
}

initializeTheme();

createRoot(root).render(
  <StrictMode>
    <BrowserRouter>
      <NuqsAdapter>
        <App />
      </NuqsAdapter>
    </BrowserRouter>
  </StrictMode>
);
