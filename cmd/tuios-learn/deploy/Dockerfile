# tuios-learn in a container. Build from the repository root:
#
#   docker build -f cmd/tuios-learn/deploy/Dockerfile -t tuios-learn .
#   docker run -d --name tuios-learn --restart=always \
#     -p 22:2222 -v tuios-learn:/state \
#     --read-only --cap-drop=ALL --security-opt=no-new-privileges \
#     --memory=3g --pids-limit=1024 tuios-learn
#
# The container listens on 2222, so it needs no capability. Docker maps the
# host's port 22 to it.

FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/tuios-learn ./cmd/tuios-learn

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/tuios-learn /tuios-learn
VOLUME /state
EXPOSE 2222
ENV GOMAXPROCS=2
HEALTHCHECK --interval=30s --timeout=5s CMD ["/tuios-learn", "health", "127.0.0.1:2222"]
ENTRYPOINT ["/tuios-learn", "--listen", ":2222", "--state-dir", "/state", "--health", "127.0.0.1:9022"]
