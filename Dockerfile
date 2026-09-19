FROM golang:1.26 AS tuios-build

WORKDIR /go/src/app
COPY . .

# Both commands. The image shipped only the multiplexer, so the web terminal
# could not be run from it at all: there was no binary to point an entrypoint
# at. They are built in one stage because they share a module and a download.
RUN go mod download &&\
  CGO_ENABLED=0 go build -o /go/bin/tuios ./cmd/tuios &&\
  CGO_ENABLED=0 go build -o /go/bin/tuios-web ./cmd/tuios-web

# RUN go vet -v
# RUN go test -v


FROM gcr.io/distroless/static-debian11:nonroot

ENV TERM=xterm-256color
COPY --from=tuios-build /go/bin/tuios /
COPY --from=tuios-build /go/bin/tuios-web /
ENTRYPOINT ["/tuios"]

# The web terminal is run by naming it, rather than by being the default:
#
#   docker run --rm -p 7681:7681 --entrypoint /tuios-web <image> \
#     --host 0.0.0.0 --auto-tls
#
# Neither the host nor --insecure is baked in, and that is the whole point of
# leaving it to the command line. tuios-web serves a shell, it has no
# authentication of its own, and it refuses a non-loopback bind in clear text
# on purpose. An image that arrived already listening on 0.0.0.0 would turn
# that refusal into a default, and anyone who ran it to see what it did would
# be publishing a terminal. The operator passes the host when they know their
# network, and passes TLS or says --insecure in the same breath.
#
# There is no EXPOSE for the same reason. The default entrypoint is the
# multiplexer, which serves no port, so a port declared here would be
# describing something this image does not do unless it is asked to.
#
# The base stays distroless nonroot. A base with a shell would need an explicit
# USER, and the container would otherwise offer a root shell to whoever reaches
# the terminal it is serving.
