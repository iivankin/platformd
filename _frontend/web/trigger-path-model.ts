export const normalizeTriggerPath = (value: string) =>
  value.trim().replace(/^\/+/u, "").replace(/\/+$/u, "");

export const triggerPathCovers = (parent: string, candidate: string) => {
  const normalizedParent = normalizeTriggerPath(parent);
  const normalizedCandidate = normalizeTriggerPath(candidate);

  if (!(normalizedParent && normalizedCandidate)) {
    return false;
  }

  return (
    normalizedCandidate === normalizedParent ||
    normalizedCandidate.startsWith(`${normalizedParent}/`)
  );
};

export const isTriggerPathCovered = (
  candidate: string,
  selectedPaths: string[]
) => selectedPaths.some((path) => triggerPathCovers(path, candidate));

export const addTriggerPath = (selectedPaths: string[], value: string) => {
  const path = normalizeTriggerPath(value);
  if (!path || isTriggerPathCovered(path, selectedPaths)) {
    return selectedPaths;
  }

  return [
    ...selectedPaths.filter((selected) => !triggerPathCovers(path, selected)),
    path,
  ];
};
