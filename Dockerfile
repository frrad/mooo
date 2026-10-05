FROM golang:1.27.1-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=1 GOFLAGS=-tags=goolm go build -trimpath -o /out/mooo-bridge ./cmd/mooo-bridge

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /data/profiles && chown -R 10001:10001 /data \
    && chmod 0700 /data /data/profiles
COPY --from=build /out/mooo-bridge /usr/local/bin/mooo-bridge
USER 10001:10001
WORKDIR /data
EXPOSE 29340
ENTRYPOINT ["/usr/local/bin/mooo-bridge"]
CMD ["-c", "/data/config.yaml", "-r", "/data/registration.yaml"]
