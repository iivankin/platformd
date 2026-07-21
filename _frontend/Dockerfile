FROM oven/bun:1.3.14 AS build

WORKDIR /src

COPY package.json bun.lock ./
RUN bun install --frozen-lockfile

COPY . .

RUN MOCK_BINARY_OUTPUT=/out/platformd-ui-mock bun run build:mock

FROM gcr.io/distroless/cc-debian12:nonroot

COPY --from=build --chown=nonroot:nonroot \
    /out/platformd-ui-mock /platformd-ui-mock

ENV HOST=0.0.0.0 \
    MOCK_SCENARIO=demo \
    PORT=3100

EXPOSE 3100

ENTRYPOINT ["/platformd-ui-mock"]
