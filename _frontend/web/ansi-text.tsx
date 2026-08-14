import type { CSSProperties } from "react";

const ansiColors = [
  "#111827",
  "#ef4444",
  "#22c55e",
  "#eab308",
  "#3b82f6",
  "#d946ef",
  "#06b6d4",
  "#d1d5db",
  "#6b7280",
  "#fb7185",
  "#4ade80",
  "#fde047",
  "#60a5fa",
  "#e879f9",
  "#22d3ee",
  "#f9fafb",
] as const;

interface AnsiState {
  background?: string;
  bold: boolean;
  dim: boolean;
  foreground?: string;
  hidden: boolean;
  inverse: boolean;
  italic: boolean;
  strike: boolean;
  underline: boolean;
}

export interface AnsiSegment {
  style: CSSProperties;
  text: string;
}

const initialState = (): AnsiState => ({
  background: undefined,
  bold: false,
  dim: false,
  foreground: undefined,
  hidden: false,
  inverse: false,
  italic: false,
  strike: false,
  underline: false,
});

const ansiChannel = (part: number) => (part === 0 ? 0 : 55 + part * 40);
const colorChannel = (value: number) => Math.max(0, Math.min(255, value));

const indexedColor = (index: number): string | undefined => {
  if (index < ansiColors.length) {
    return ansiColors[index];
  }
  if (index >= 16 && index <= 231) {
    const value = index - 16;
    const red = ansiChannel(Math.floor(value / 36));
    const green = ansiChannel(Math.floor((value % 36) / 6));
    const blue = ansiChannel(value % 6);
    return `rgb(${red.toString()} ${green.toString()} ${blue.toString()})`;
  }
  if (index >= 232 && index <= 255) {
    const value = 8 + (index - 232) * 10;
    return `rgb(${value.toString()} ${value.toString()} ${value.toString()})`;
  }
};

const cssStyle = (state: AnsiState): CSSProperties => {
  const decorations = [
    state.underline ? "underline" : "",
    state.strike ? "line-through" : "",
  ].filter(Boolean);
  return {
    backgroundColor: state.inverse ? state.foreground : state.background,
    color: state.inverse ? state.background : state.foreground,
    fontStyle: state.italic ? "italic" : undefined,
    fontWeight: state.bold ? 700 : undefined,
    opacity: state.dim ? 0.65 : undefined,
    textDecoration: decorations.length > 0 ? decorations.join(" ") : undefined,
    visibility: state.hidden ? "hidden" : undefined,
  };
};

const extendedColor = (codes: number[], index: number) => {
  const mode = codes.at(index + 1);
  const redOrIndex = codes.at(index + 2);
  if (mode === 5 && redOrIndex !== undefined) {
    return { color: indexedColor(redOrIndex), consumed: 2 };
  }
  const green = codes.at(index + 3);
  const blue = codes.at(index + 4);
  if (
    mode === 2 &&
    redOrIndex !== undefined &&
    green !== undefined &&
    blue !== undefined
  ) {
    return {
      color: `rgb(${colorChannel(redOrIndex).toString()} ${colorChannel(green).toString()} ${colorChannel(blue).toString()})`,
      consumed: 4,
    };
  }
  return { color: undefined, consumed: 0 };
};

const sgrActions = new Map<number, (state: AnsiState) => void>([
  [0, (state) => Object.assign(state, initialState())],
  [1, (state) => (state.bold = true)],
  [2, (state) => (state.dim = true)],
  [3, (state) => (state.italic = true)],
  [4, (state) => (state.underline = true)],
  [7, (state) => (state.inverse = true)],
  [8, (state) => (state.hidden = true)],
  [9, (state) => (state.strike = true)],
  [
    22,
    (state) => {
      state.bold = false;
      state.dim = false;
    },
  ],
  [23, (state) => (state.italic = false)],
  [24, (state) => (state.underline = false)],
  [27, (state) => (state.inverse = false)],
  [28, (state) => (state.hidden = false)],
  [29, (state) => (state.strike = false)],
  [39, (state) => (state.foreground = undefined)],
  [49, (state) => (state.background = undefined)],
]);

const applyColor = (
  state: AnsiState,
  codes: number[],
  index: number,
  code: number
) => {
  if (code >= 30 && code <= 37) {
    state.foreground = indexedColor(code - 30);
    return 0;
  }
  if (code >= 90 && code <= 97) {
    state.foreground = indexedColor(code - 90 + 8);
    return 0;
  }
  if (code >= 40 && code <= 47) {
    state.background = indexedColor(code - 40);
    return 0;
  }
  if (code >= 100 && code <= 107) {
    state.background = indexedColor(code - 100 + 8);
    return 0;
  }
  if (code === 38 || code === 48) {
    const extended = extendedColor(codes, index);
    if (code === 38) {
      state.foreground = extended.color;
    } else {
      state.background = extended.color;
    }
    return extended.consumed;
  }
  return 0;
};

const applySgr = (state: AnsiState, parameters: string) => {
  const codes =
    parameters === ""
      ? [0]
      : parameters
          .split(/[;:]/u)
          .filter((value) => value !== "")
          .map(Number);
  for (let index = 0; index < codes.length; index += 1) {
    const code = codes[index] ?? 0;
    const action = sgrActions.get(code);
    if (action) {
      action(state);
      continue;
    }
    index += applyColor(state, codes, index, code);
  }
};

// Besides real ESC/CSI bytes, accept U+FFFD before `[` because some container
// runtimes replace the control byte while preserving the rest of an SGR code.
const controlSequence =
  /(?:(?:\u001B|\uFFFD)\][^\u0007]*(?:\u0007|(?:\u001B|\uFFFD)\\)|(?:\u001B\[|\u009B|\uFFFD\[)(?<parameters>[0-?]*)(?<intermediates>[ -/]*)(?<command>[@-~]))/giu; // oxlint-disable-line eslint/no-control-regex -- ANSI parser intentionally matches ESC and CSI bytes.

export const parseAnsi = (text: string): AnsiSegment[] => {
  const state = initialState();
  const segments: AnsiSegment[] = [];
  let offset = 0;
  for (const match of text.matchAll(controlSequence)) {
    const index = match.index ?? 0;
    if (index > offset) {
      segments.push({
        style: cssStyle(state),
        text: text.slice(offset, index),
      });
    }
    if (match.groups?.command === "m") {
      applySgr(state, match.groups.parameters ?? "");
    }
    offset = index + match[0].length;
  }
  if (offset < text.length) {
    segments.push({ style: cssStyle(state), text: text.slice(offset) });
  }
  return segments;
};

export const AnsiText = ({ text }: { text: string }) =>
  parseAnsi(text).map((segment, index) => (
    <span
      key={`${index.toString()}:${segment.text.slice(0, 12)}`}
      style={segment.style}
    >
      {segment.text}
    </span>
  ));
