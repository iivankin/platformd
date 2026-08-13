FROM golang:1.25.1-bookworm AS build
WORKDIR /src
COPY go.mod go.sum main.go ./
RUN CGO_ENABLED=0 go build -trimpath -o /out/conformance .
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/conformance /conformance
ENTRYPOINT ["/conformance"]
