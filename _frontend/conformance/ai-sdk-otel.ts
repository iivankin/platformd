import { mkdir, mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";

import { LegacyOpenTelemetry, OpenTelemetry } from "@ai-sdk/otel";
import { OTLPTraceExporter } from "@opentelemetry/exporter-trace-otlp-proto";
import { SimpleSpanProcessor } from "@opentelemetry/sdk-trace-base";
import { NodeTracerProvider } from "@opentelemetry/sdk-trace-node";
import {
  embed,
  generateText,
  rerank,
  simulateReadableStream,
  streamText,
  tool,
} from "ai";
import {
  MockEmbeddingModelV4,
  MockLanguageModelV4,
  MockRerankingModelV4,
} from "ai/test";
import { z } from "zod";

const requestBodies: Uint8Array[] = [];
const server = Bun.serve({
  async fetch(request) {
    if (
      request.method !== "POST" ||
      new URL(request.url).pathname !== "/v1/traces"
    ) {
      return new Response(null, { status: 404 });
    }
    requestBodies.push(new Uint8Array(await request.arrayBuffer()));
    return new Response(null, { status: 200 });
  },
  hostname: "127.0.0.1",
  port: 0,
});
const exporter = new OTLPTraceExporter({
  url: `http://${server.hostname}:${server.port}/v1/traces`,
});
const provider = new NodeTracerProvider({
  spanProcessors: [new SimpleSpanProcessor(exporter)],
});
provider.register();
const tracer = provider.getTracer("platformd-ai-sdk-conformance");
const integrations = [
  new OpenTelemetry({ runtimeContext: true, usage: true }),
  new LegacyOpenTelemetry(),
];
const telemetry = (functionId: string) => ({
  functionId,
  includeRuntimeContext: { sessionId: true, userId: true },
  integrations,
});
const usage = ({
  cacheRead = 0,
  cacheWrite = 0,
  input,
  output,
  reasoning = 0,
}: {
  cacheRead?: number;
  cacheWrite?: number;
  input: number;
  output: number;
  reasoning?: number;
}) => ({
  inputTokens: {
    cacheRead,
    cacheWrite,
    noCache: input - cacheRead - cacheWrite,
    total: input,
  },
  outputTokens: {
    reasoning,
    text: output - reasoning,
    total: output,
  },
});

const temporaryDirectory = await mkdtemp(
  path.join(tmpdir(), "platformd-ai-conformance-")
);
const fixturePath = path.join(temporaryDirectory, "otlp-requests");

try {
  const generated = await generateText({
    model: new MockLanguageModelV4({
      doGenerate: {
        content: [{ text: "generated response", type: "text" }],
        finishReason: { raw: "stop", unified: "stop" },
        response: { id: "generate-response", modelId: "gpt-conformance" },
        usage: usage({
          cacheRead: 200,
          input: 1000,
          output: 100,
          reasoning: 20,
        }),
        warnings: [],
      },
      modelId: "gpt-conformance",
      provider: "openai.chat",
    }),
    prompt: "generate a conformance response",
    runtimeContext: { sessionId: "session-generate", userId: "user-42" },
    telemetry: telemetry("conformance.generate"),
  });
  if (generated.text !== "generated response") {
    throw new Error(`unexpected generateText output: ${generated.text}`);
  }

  const streamed = streamText({
    model: new MockLanguageModelV4({
      doStream: {
        stream: simulateReadableStream({
          chunkDelayInMs: 5,
          chunks: [
            { type: "stream-start" as const, warnings: [] },
            { id: "stream-text", type: "text-start" as const },
            {
              delta: "streamed ",
              id: "stream-text",
              type: "text-delta" as const,
            },
            {
              delta: "response",
              id: "stream-text",
              type: "text-delta" as const,
            },
            { id: "stream-text", type: "text-end" as const },
            {
              finishReason: { raw: "stop", unified: "stop" as const },
              type: "finish" as const,
              usage: usage({ input: 300, output: 30 }),
            },
          ],
          initialDelayInMs: 15,
        }),
      },
      modelId: "gpt-stream-conformance",
      provider: "anthropic.messages",
    }),
    prompt: "stream a conformance response",
    runtimeContext: { sessionId: "session-stream", userId: "user-42" },
    telemetry: telemetry("conformance.stream"),
  });
  const streamedText = await streamed.text;
  if (streamedText !== "streamed response") {
    throw new Error(`unexpected streamText output: ${streamedText}`);
  }

  const toolResult = await generateText({
    model: new MockLanguageModelV4({
      doGenerate: {
        content: [
          {
            input: JSON.stringify({ query: "SELECT 42" }),
            toolCallId: "database-call",
            toolName: "run_database_query",
            type: "tool-call",
          },
        ],
        finishReason: { raw: "tool_calls", unified: "tool-calls" },
        response: { id: "tool-response", modelId: "gpt-tool-conformance" },
        usage: usage({ input: 500, output: 50 }),
        warnings: [],
      },
      modelId: "gpt-tool-conformance",
      provider: "openai.responses",
    }),
    prompt: "run the database query",
    runtimeContext: { sessionId: "session-tool", userId: "user-42" },
    telemetry: telemetry("conformance.tool"),
    tools: {
      run_database_query: tool({
        execute: async ({ query }) =>
          await tracer.startActiveSpan(
            "SELECT conformance_child",
            {
              attributes: {
                "db.operation.name": "SELECT",
                "db.query.text": query,
              },
            },
            (span) => {
              try {
                return { rows: [{ answer: 42 }] };
              } finally {
                span.end();
              }
            }
          ),
        inputSchema: z.object({ query: z.string() }),
      }),
    },
  });
  if (toolResult.toolResults.length !== 1) {
    throw new Error(
      `expected one executed tool, received ${toolResult.toolResults.length}`
    );
  }

  await embed({
    model: new MockEmbeddingModelV4({
      doEmbed: {
        embeddings: [[0.1, 0.2]],
        usage: { tokens: 1000 },
        warnings: [],
      },
      modelId: "embed-test",
      provider: "azure-openai.chat",
    }),
    telemetry: telemetry("conformance.embed"),
    value: "platformd telemetry conformance",
  });
  await rerank({
    documents: ["irrelevant", "relevant"],
    model: new MockRerankingModelV4({
      doRerank: () =>
        Promise.resolve({
          ranking: [
            { index: 1, relevanceScore: 0.9 },
            { index: 0, relevanceScore: 0.1 },
          ],
          warnings: [],
        }),
      modelId: "rerank-test",
      provider: "cohere",
    }),
    query: "relevant",
    telemetry: telemetry("conformance.rerank"),
  });

  await provider.forceFlush();
  if (requestBodies.length <= 1) {
    throw new Error(
      `expected separate OTLP exports, received ${requestBodies.length}`
    );
  }
  await mkdir(fixturePath);
  await Promise.all(
    requestBodies.map((body, index) =>
      Bun.write(path.join(fixturePath, `${index}.pb`), body)
    )
  );

  const repositoryRoot = path.resolve(import.meta.dir, "../..");
  const test = Bun.spawn(
    [
      "cargo",
      "test",
      "--release",
      "--locked",
      "--manifest-path",
      "telemetry/Cargo.toml",
      "ai_sdk_otlp_conformance",
      "--",
      "--ignored",
      "--nocapture",
    ],
    {
      cwd: repositoryRoot,
      env: {
        ...process.env,
        PLATFORMD_AI_SDK_OTLP_FIXTURE: fixturePath,
      },
      stderr: "inherit",
      stdout: "inherit",
    }
  );
  const exitCode = await test.exited;
  if (exitCode !== 0) {
    throw new Error(`Rust conformance test failed with exit code ${exitCode}`);
  }
} finally {
  await provider.shutdown();
  server.stop(true);
  await rm(temporaryDirectory, { force: true, recursive: true });
}
