# syntax=docker/dockerfile:1.12

FROM rust:1.97.1-bookworm AS build
WORKDIR /src
COPY Cargo.toml Cargo.lock ./
COPY src ./src
RUN cargo build --release --locked

FROM debian:bookworm-slim AS runtime
RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /src/target/release/error-tracker /usr/local/bin/error-tracker
RUN useradd --create-home --uid 10001 --shell /usr/sbin/nologin error-tracker \
    && mkdir /data \
    && chown error-tracker:error-tracker /data
USER error-tracker
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=5s --start-period=30s --retries=6 \
    CMD ["curl", "--fail", "--silent", "--show-error", "http://127.0.0.1:8080/health"]
ENV ERROR_TRACKER_LISTEN=0.0.0.0:8080 \
    ERROR_TRACKER_VOLUME=/data
ENTRYPOINT ["/usr/local/bin/error-tracker"]
