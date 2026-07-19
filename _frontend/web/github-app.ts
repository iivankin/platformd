export const githubAppInstallationURL = (appSlug: string) => {
  const slug = appSlug.trim();
  return slug
    ? `https://github.com/apps/${encodeURIComponent(slug)}/installations/new`
    : undefined;
};
