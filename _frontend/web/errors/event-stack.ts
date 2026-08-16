import { asRecord } from "./event-context";
import type { EventDetail } from "./types";

export interface StackFrame {
  colno?: number;
  contextLine?: string;
  filename?: string;
  functionName?: string;
  inApp?: boolean;
  lineno?: number;
  module?: string;
  postContext: string[];
  preContext: string[];
  sourceMap?: string;
  symbolicated?: boolean;
}

export interface StackMechanism {
  data: { name: string; value: string }[];
  description?: string;
  exceptionId?: number;
  handled?: boolean;
  helpLink?: string;
  isExceptionGroup: boolean;
  parentId?: number;
  source?: string;
  type?: string;
}

export interface StackGroup {
  frames: StackFrame[];
  label: string;
  mechanism?: StackMechanism;
  relationship?: string;
}

const stringArray = (value: unknown): string[] =>
  Array.isArray(value)
    ? value.filter((line): line is string => typeof line === "string")
    : [];

const optionalString = (value: unknown) =>
  typeof value === "string" && value !== "" ? value : undefined;

const optionalNumber = (value: unknown) =>
  typeof value === "number" && Number.isFinite(value) ? value : undefined;

const scalar = (value: unknown) =>
  typeof value === "string" || typeof value === "number"
    ? String(value)
    : undefined;

const optionalHttpUrl = (value: unknown) => {
  const candidate = optionalString(value);
  if (!candidate) {
    return;
  }
  try {
    const url = new URL(candidate);
    return ["http:", "https:"].includes(url.protocol) ? candidate : undefined;
  } catch {
    // Malformed and non-HTTP mechanism links are intentionally hidden.
  }
};

const primitiveEntries = (value: unknown) =>
  Object.entries(asRecord(value) ?? {}).flatMap(([name, entry]) =>
    ["string", "number", "boolean"].includes(typeof entry)
      ? [{ name, value: String(entry) }]
      : []
  );

const mechanismMetadata = (value: unknown) => {
  const meta = asRecord(value);
  const errno = asRecord(meta?.errno);
  const machException = asRecord(meta?.mach_exception);
  const signal = asRecord(meta?.signal);
  const signalName = scalar(signal?.name ?? signal?.number);
  const signalCode = scalar(signal?.code_name ?? signal?.code);
  const signalValue =
    signalName && signalCode ? `${signalName} (${signalCode})` : signalName;
  const entries: [string, string | undefined][] = [
    ["errno", scalar(errno?.name ?? errno?.number)],
    ["mach exception", scalar(machException?.name ?? machException?.exception)],
    ["signal", signalValue],
  ];
  return entries.flatMap(([name, entry]) =>
    entry === undefined ? [] : [{ name, value: entry }]
  );
};

export const framesFromStacktrace = (value: unknown): StackFrame[] => {
  const frames = asRecord(value)?.frames;
  if (!Array.isArray(frames)) {
    return [];
  }
  return frames.flatMap((frame) => {
    const record = asRecord(frame);
    if (!record) {
      return [];
    }
    const data = asRecord(record.data);
    return [
      {
        colno: typeof record.colno === "number" ? record.colno : undefined,
        contextLine:
          typeof record.context_line === "string"
            ? record.context_line
            : undefined,
        filename:
          typeof record.filename === "string" ? record.filename : undefined,
        functionName:
          typeof record.function === "string" ? record.function : undefined,
        inApp: typeof record.in_app === "boolean" ? record.in_app : undefined,
        lineno: typeof record.lineno === "number" ? record.lineno : undefined,
        module: typeof record.module === "string" ? record.module : undefined,
        postContext: stringArray(record.post_context),
        preContext: stringArray(record.pre_context),
        sourceMap: optionalString(data?.sourcemap),
        symbolicated: data?.symbolicated === true,
      },
    ];
  });
};

export interface SourceLine {
  active: boolean;
  number?: number;
  text: string;
}

export const sourceContextLines = (frame: StackFrame): SourceLine[] => {
  if (frame.contextLine === undefined) {
    return [];
  }
  const firstLine = frame.lineno
    ? Math.max(1, frame.lineno - frame.preContext.length)
    : undefined;
  return [
    ...frame.preContext.map((text, index) => ({
      active: false,
      number: firstLine === undefined ? undefined : firstLine + index,
      text,
    })),
    {
      active: true,
      number: frame.lineno,
      text: frame.contextLine,
    },
    ...frame.postContext.map((text, index) => ({
      active: false,
      number: frame.lineno === undefined ? undefined : frame.lineno + index + 1,
      text,
    })),
  ];
};

const mechanismFromException = (
  exception: Record<string, unknown> | undefined
): StackMechanism | undefined => {
  const mechanism = asRecord(exception?.mechanism);
  if (!mechanism) {
    return;
  }
  return {
    data: [
      ...mechanismMetadata(mechanism.meta),
      ...primitiveEntries(mechanism.data),
    ],
    description: optionalString(mechanism.description),
    exceptionId: optionalNumber(mechanism.exception_id),
    handled:
      typeof mechanism.handled === "boolean" ? mechanism.handled : undefined,
    helpLink: optionalHttpUrl(mechanism.help_link),
    isExceptionGroup: mechanism.is_exception_group === true,
    parentId: optionalNumber(mechanism.parent_id),
    source: optionalString(mechanism.source),
    type: optionalString(mechanism.type),
  };
};

const exceptionLabel = (
  exception: Record<string, unknown> | undefined,
  fallback: string
) => {
  const label = [exception?.type, exception?.value]
    .filter((entry): entry is string => typeof entry === "string")
    .join(": ");
  return label || fallback;
};

const withRelationships = (groups: StackGroup[]) =>
  groups.map((group) => {
    const { exceptionId, isExceptionGroup, parentId } = group.mechanism ?? {};
    const parent = groups.find(
      (candidate) => candidate.mechanism?.exceptionId === parentId
    );
    const childCount = groups.filter(
      (candidate) => candidate.mechanism?.parentId === exceptionId
    ).length;
    let relationship: string | undefined;
    if (parent) {
      relationship = `Child of ${parent.label}`;
    } else if (isExceptionGroup && childCount > 0) {
      relationship = `${childCount} related exception${childCount === 1 ? "" : "s"}`;
    }
    return { ...group, relationship };
  });

const selectedThreadStack = (payload: Record<string, unknown>) => {
  const values = asRecord(payload.threads)?.values;
  if (!Array.isArray(values)) {
    return;
  }
  const threads = values.flatMap((value) => {
    const thread = asRecord(value);
    return thread ? [thread] : [];
  });
  return (
    threads.find((thread) => thread.crashed === true) ??
    threads.find((thread) => asRecord(thread.stacktrace) !== undefined) ??
    threads[0]
  );
};

const exceptionThreadId = (exception: Record<string, unknown>) =>
  exception.thread_id ?? exception.threadId;

const symbolicatedStackGroups = (
  detail: EventDetail,
  exceptions: (Record<string, unknown> | undefined)[]
) => {
  const symbolicated = asRecord(detail.symbolication?.payload)?.stacktraces;
  if (!Array.isArray(symbolicated)) {
    return [];
  }
  return withRelationships(
    symbolicated.flatMap((stacktrace, index) => {
      const frames = framesFromStacktrace(stacktrace);
      const exception = exceptions[index];
      return frames.length === 0
        ? []
        : [
            {
              frames,
              label: exceptionLabel(
                exception,
                `Symbolicated stack ${index + 1}`
              ),
              mechanism: mechanismFromException(exception),
            },
          ];
    })
  );
};

const exceptionStackGroups = (
  exceptions: (Record<string, unknown> | undefined)[]
) =>
  withRelationships(
    exceptions.flatMap((exception, index) => {
      const frames = framesFromStacktrace(exception?.stacktrace);
      return frames.length === 0
        ? []
        : [
            {
              frames,
              label: exceptionLabel(exception, `Exception ${index + 1}`),
              mechanism: mechanismFromException(exception),
            },
          ];
    })
  );

const matchedThreadException = (
  thread: Record<string, unknown>,
  exceptions: (Record<string, unknown> | undefined)[]
) => {
  const exception = exceptions.length === 1 ? exceptions[0] : undefined;
  if (!exception) {
    return;
  }
  if (exceptionThreadId(exception) === thread.id) {
    return exception;
  }
  const hasThreadIds = exceptions.some(
    (candidate) => candidate && exceptionThreadId(candidate) !== undefined
  );
  return thread.crashed === true && !hasThreadIds ? exception : undefined;
};

const threadStackGroup = (
  payload: Record<string, unknown>,
  exceptions: (Record<string, unknown> | undefined)[]
): StackGroup[] => {
  const thread = selectedThreadStack(payload);
  if (!thread) {
    return [];
  }
  const frames = framesFromStacktrace(thread.stacktrace);
  if (frames.length === 0) {
    return [];
  }
  const exception = matchedThreadException(thread, exceptions);
  const threadName = optionalString(thread.name);
  const fallbackLabel = threadName
    ? `Thread ${threadName}`
    : `Thread ${thread.id === undefined ? "stack" : String(thread.id)}`;
  return [
    {
      frames,
      label: exception ? exceptionLabel(exception, "Exception") : fallbackLabel,
      mechanism: mechanismFromException(exception),
    },
  ];
};

export const eventStackGroups = (detail: EventDetail): StackGroup[] => {
  const payload = asRecord(detail.event.payload) ?? {};
  const exceptionValues = asRecord(payload.exception)?.values;
  const exceptions = Array.isArray(exceptionValues)
    ? exceptionValues.map(asRecord)
    : [];
  const symbolicated = symbolicatedStackGroups(detail, exceptions);
  if (symbolicated.length > 0) {
    return symbolicated;
  }
  const exceptionGroups = exceptionStackGroups(exceptions);
  if (exceptionGroups.length > 0) {
    return exceptionGroups;
  }
  const eventFrames = framesFromStacktrace(payload.stacktrace);
  if (eventFrames.length > 0) {
    return [{ frames: eventFrames, label: "Event stack" }];
  }
  return threadStackGroup(payload, exceptions);
};

// Sentry's Relevant view keeps one system boundary frame around in-app code.
// If a particular chained exception has no in-app frames, its full stack stays
// visible instead of making that exception disappear from the chain.
export const relevantStackFrames = (frames: StackFrame[]) => {
  if (!frames.some((frame) => frame.inApp === true)) {
    return frames;
  }
  return frames.filter((frame, index) => {
    const next = frames[index + 1];
    return frame.inApp === true || next?.inApp === true || next === undefined;
  });
};
