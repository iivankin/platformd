const webhookSecretBytes = 32;

export const createGitHubWebhookSecret = () => {
  const bytes = crypto.getRandomValues(new Uint8Array(webhookSecretBytes));
  let binary = "";
  for (const byte of bytes) {
    binary += String.fromCodePoint(byte);
  }
  return btoa(binary)
    .replaceAll("+", "-")
    .replaceAll("/", "_")
    .replace(/=+$/u, "");
};
