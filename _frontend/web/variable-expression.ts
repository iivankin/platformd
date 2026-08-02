export interface VariableReference {
  output: string;
  resource: string;
}

const resourceName = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/u;
const outputName = /^[A-Za-z_][A-Za-z0-9_]*$/u;
const referenceStart = "${{";
const referenceEnd = "}}";

const parseReference = (value: string): VariableReference => {
  const separator = value.indexOf(".");
  const resource = value.slice(0, separator);
  const output = value.slice(separator + 1);
  if (
    separator === -1 ||
    output.includes(".") ||
    !resourceName.test(resource) ||
    !outputName.test(output)
  ) {
    throw new Error(`Invalid variable reference: ${value}`);
  }
  return { output, resource };
};

export const variableReferences = (value: string): VariableReference[] => {
  const references: VariableReference[] = [];
  let remaining = value;
  while (remaining) {
    const start = remaining.indexOf(referenceStart);
    if (start === -1) {
      return references;
    }
    remaining = remaining.slice(start + referenceStart.length);
    const end = remaining.indexOf(referenceEnd);
    if (end === -1) {
      throw new Error("Unterminated variable reference");
    }
    references.push(parseReference(remaining.slice(0, end).trim()));
    remaining = remaining.slice(end + referenceEnd.length);
  }
  return references;
};
