FROM golang:1.24 as tuios-build

WORKDIR /go/src/app
COPY . .

RUN go mod download &&\
  CGO_ENABLED=0 go build -o /go/bin/tuios

# RUN go vet -v
# RUN go test -v


FROM gcr.io/distroless/static-debian11:nonroot

ENV TERM=xterm-256color
COPY --from=tuios-build /go/bin/tuios /
ENTRYPOINT ["/tuios"]


