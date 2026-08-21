const countryDisplayNames = (() => {
  try {
    return new Intl.DisplayNames(["en"], { type: "region" });
  } catch {
    // Older browsers keep the original country code below.
  }
})();

export const analyticsCountryName = (code: string) => {
  const value = code.trim();
  if (!value) {
    return "";
  }
  try {
    return countryDisplayNames?.of(value.toUpperCase()) ?? value;
  } catch {
    return value;
  }
};

export const analyticsLocationName = ({
  city,
  country,
  region,
}: {
  city: string;
  country: string;
  region: string;
}) => {
  const seen = new Set<string>();
  const parts = [
    city.trim(),
    region.trim(),
    analyticsCountryName(country),
  ].filter((part) => {
    const key = part.toLocaleLowerCase();
    if (!part || seen.has(key)) {
      return false;
    }
    seen.add(key);
    return true;
  });
  return parts.join(", ") || "Unknown";
};
