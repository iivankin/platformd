import { expect, test } from "bun:test";

import { githubAppInstallationURL } from "@/github-app";

test("builds the GitHub App installation target URL", () => {
  expect(githubAppInstallationURL("platformd-app")).toBe(
    "https://github.com/apps/platformd-app/installations/new"
  );
  expect(githubAppInstallationURL(" ")).toBeUndefined();
});
