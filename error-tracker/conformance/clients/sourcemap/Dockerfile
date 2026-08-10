FROM debian:bookworm-slim
ARG TARGETARCH
ARG SENTRY_CLI_VERSION=3.6.2
RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && case "$TARGETARCH" in \
      amd64) cli_arch=x86_64; cli_sha=3a4bbf2c0d06378d4e59b337647483751a0a2b1603db5fd4991847d0cfd6478c ;; \
      arm64) cli_arch=aarch64; cli_sha=ff112ecf694b7d6b3629a6228ed4e3f7a0d51401bdf48a5051a79d8749dccd06 ;; \
      *) echo "unsupported architecture: $TARGETARCH" >&2; exit 1 ;; \
    esac \
    && curl --fail --location --retry 3 --output /usr/local/bin/sentry-cli \
      "https://github.com/getsentry/sentry-cli/releases/download/${SENTRY_CLI_VERSION}/sentry-cli-Linux-${cli_arch}" \
    && echo "${cli_sha}  /usr/local/bin/sentry-cli" | sha256sum --check --status \
    && chmod 0755 /usr/local/bin/sentry-cli
WORKDIR /case
COPY dist ./dist
COPY run.sh ./run.sh
ENTRYPOINT ["/case/run.sh"]
