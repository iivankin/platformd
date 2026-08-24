export const traceInteger = (value: string) => globalThis.BigInt(value);

export const traceNanosToMilliseconds = (value: string) =>
  Number(traceInteger(value)) / 1_000_000;

export const traceNanosToDate = (value: string) =>
  new Date(Number(traceInteger(value) / 1_000_000n));

export const formatTraceDuration = (value: string) => {
  const milliseconds = traceNanosToMilliseconds(value);
  if (milliseconds < 1) {
    return `${Math.round(milliseconds * 1000)} μs`;
  }
  if (milliseconds < 1000) {
    return `${milliseconds.toFixed(milliseconds < 10 ? 2 : 1)} ms`;
  }
  return `${(milliseconds / 1000).toFixed(2)} s`;
};
