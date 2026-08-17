FROM golang:1.25 AS tuios-build

WORKDIR /go/src/app
COPY . .

RUN go mod download && \
    CGO_ENABLED=0 go build -o /go/bin/tuios-web ./cmd/tuios-web && \
    CGO_ENABLED=0 go build -o /go/bin/tuios ./cmd/tuios

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    bash \
    procps \
    && rm -rf /var/lib/apt/lists/*

ENV TERM=xterm-256color \
    COLORTERM=truecolor \
    PORT=7681

COPY --from=tuios-build /go/bin/tuios-web /usr/local/bin/tuios-web
COPY --from=tuios-build /go/bin/tuios /usr/local/bin/tuios

EXPOSE 7681 8000 3000

CMD ["sh", "-c", "exec /usr/local/bin/tuios-web --host 0.0.0.0 --port \"${PORT:-7681}\" --insecure"]



