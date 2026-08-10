FROM rust:1.97.1-bookworm AS build
WORKDIR /src
COPY Cargo.toml Cargo.lock main.rs ./
RUN cargo build --release --locked
FROM gcr.io/distroless/cc-debian12:nonroot
COPY --from=build /src/target/release/error-tracker-conformance-rust /conformance
ENTRYPOINT ["/conformance"]
