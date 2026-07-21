import {
  Check,
  Copy,
  ExternalLink,
  GitFork,
  KeyRound,
  Webhook,
} from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import { githubAppInstallationURL } from "@/github-app";

const CREATE_APP_URL = "https://github.com/settings/apps/new";
const GITHUB_APPS_URL = "https://github.com/settings/apps";
const SETUP_DOCS_URL =
  "https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app";

const ExternalAction = ({
  children,
  href,
}: {
  children: React.ReactNode;
  href: string;
}) => (
  <a
    className="inline-flex h-7 items-center justify-center gap-1.5 border border-border bg-background px-2 text-[10px] font-medium text-foreground transition-colors hover:bg-muted"
    href={href}
    rel="noreferrer"
    target="_blank"
  >
    {children}
    <ExternalLink className="size-3" />
  </a>
);

const CopyValue = ({
  emptyText,
  label,
  value,
}: {
  emptyText?: string;
  label: string;
  value?: string;
}) => {
  const [copied, setCopied] = useState(false);

  const copy = async () => {
    if (!value) {
      return;
    }
    await navigator.clipboard.writeText(value);
    setCopied(true);
    globalThis.setTimeout(() => setCopied(false), 1500);
  };

  return (
    <div className="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] border border-border bg-background">
      <div className="min-w-0 px-3 py-2">
        <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
          {label}
        </p>
        <code
          className={`mt-1 block truncate text-[9px] ${value ? "" : "text-muted-foreground"}`}
          title={value ?? emptyText}
        >
          {value ?? emptyText}
        </code>
      </div>
      <Button
        aria-label={`Copy ${label}`}
        className="h-full border-y-0 border-r-0"
        disabled={!value}
        onClick={() => void copy()}
        size="icon"
        type="button"
        variant="outline"
      >
        {copied ? <Check /> : <Copy />}
      </Button>
    </div>
  );
};

const SetupStep = ({
  children,
  description,
  number,
  title,
}: {
  children: React.ReactNode;
  description: string;
  number: string;
  title: string;
}) => (
  <div className="grid border-b border-border last:border-b-0 md:grid-cols-[13rem_minmax(0,1fr)]">
    <div className="border-b border-border px-5 py-4 md:border-r md:border-b-0">
      <p className="text-[8px] tracking-[0.14em] text-muted-foreground uppercase">
        Step {number}
      </p>
      <p className="mt-1 text-[11px] font-medium">{title}</p>
      <p className="mt-1 text-[9px] leading-4 text-muted-foreground">
        {description}
      </p>
    </div>
    <div className="min-w-0 px-5 py-4">{children}</div>
  </div>
);

export const GitHubAppSetupGuide = ({
  appSlug,
  homepageURL,
  webhookURL,
}: {
  appSlug?: string;
  homepageURL: string;
  webhookURL?: string;
}) => {
  const installURL = githubAppInstallationURL(appSlug ?? "");

  return (
    <SectionCard>
      <div className="flex items-start gap-3 border-b border-border px-5 py-4">
        <GitFork className="mt-0.5 size-4 text-muted-foreground" />
        <div>
          <h2 className="text-xs font-medium">Create the GitHub App</h2>
          <p className="mt-1 text-[9px] leading-4 text-muted-foreground">
            Create one app, save its credentials below, then install it for the
            repositories platformd may build.
          </p>
        </div>
        <div className="ml-auto hidden sm:block">
          <ExternalAction href={SETUP_DOCS_URL}>GitHub guide</ExternalAction>
        </div>
      </div>

      <SetupStep
        description="For organization workloads, register the app under the organization so its ownership is not tied to one person."
        number="01"
        title="Register the app"
      >
        <div className="grid gap-3">
          <div className="flex flex-wrap items-center gap-2">
            <ExternalAction href={CREATE_APP_URL}>
              Open GitHub registration
            </ExternalAction>
            <ExternalAction href={GITHUB_APPS_URL}>
              Your GitHub Apps
            </ExternalAction>
          </div>
          <p className="text-[9px] leading-4 text-muted-foreground">
            App ownership controls who can manage its keys and settings.
            Repository access is granted separately when the app is installed,
            for all or selected repositories.
          </p>
        </div>
      </SetupStep>

      <SetupStep
        description="Keep webhooks active. GitHub must be able to reach the webhook over public HTTPS."
        number="02"
        title="Copy the URLs"
      >
        <div className="grid gap-2 lg:grid-cols-2">
          <CopyValue label="Homepage URL" value={homepageURL} />
          <CopyValue
            emptyText="Configure an Automation hostname in General settings"
            label="Webhook URL"
            value={webhookURL}
          />
        </div>
      </SetupStep>

      <SetupStep
        description="These are the minimum permissions used for source archives, CI state, and GitHub deployment history."
        number="03"
        title="Choose access"
      >
        <div className="grid gap-4 lg:grid-cols-2">
          <div>
            <p className="flex items-center gap-1.5 text-[10px] font-medium">
              <KeyRound className="size-3 text-muted-foreground" />
              Repository permissions
            </p>
            <dl className="mt-2 grid grid-cols-[1fr_auto] gap-x-5 gap-y-1 text-[9px]">
              <dt>Contents</dt>
              <dd className="text-muted-foreground">Read-only</dd>
              <dt>Checks</dt>
              <dd className="text-muted-foreground">Read-only</dd>
              <dt>Commit statuses</dt>
              <dd className="text-muted-foreground">Read-only</dd>
              <dt>Deployments</dt>
              <dd className="text-muted-foreground">Read and write</dd>
              <dt>Issues</dt>
              <dd className="text-muted-foreground">Read and write</dd>
              <dt>Pull requests</dt>
              <dd className="text-muted-foreground">Read-only</dd>
              <dt>Metadata</dt>
              <dd className="text-muted-foreground">Read-only</dd>
            </dl>
          </div>
          <div>
            <p className="flex items-center gap-1.5 text-[10px] font-medium">
              <Webhook className="size-3 text-muted-foreground" />
              Subscribe to events
            </p>
            <p className="mt-2 text-[9px] leading-5 text-muted-foreground">
              Push · Pull request · Check run · Check suite
            </p>
            <p className="mt-1 text-[9px] leading-4 text-muted-foreground">
              Choose “Only on this account” unless other GitHub accounts must
              install the app.
            </p>
          </div>
        </div>
      </SetupStep>

      <SetupStep
        description="Use the same webhook secret in GitHub and below. GitHub generates the private key as a PEM file."
        number="04"
        title="Save and install"
      >
        <div className="flex flex-wrap items-center gap-2">
          <p className="mr-auto max-w-2xl text-[9px] leading-4 text-muted-foreground">
            Copy the App ID, generate a private key in the app settings, enter
            both credentials below, then install the verified app for all or
            selected repositories.
          </p>
          {installURL ? (
            <ExternalAction href={installURL}>Install app</ExternalAction>
          ) : (
            <span className="text-[9px] text-muted-foreground">
              Install link appears after verification
            </span>
          )}
        </div>
      </SetupStep>
    </SectionCard>
  );
};
