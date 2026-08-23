ARG GO_VERSION_TAG=1.25-alpine

# build
FROM golang:${GO_VERSION_TAG} AS builder

WORKDIR /build

COPY go.mod go.sum *.go ./
COPY examples/go.mod examples/go.sum* examples/*.go ./examples/

WORKDIR /build/examples
RUN go mod tidy \
    && go mod download \
    && go build -o /extproc .

# application
FROM golang:${GO_VERSION_TAG}

COPY --from=builder /extproc /extproc

EXPOSE 50051
EXPOSE 9090

ARG EXAMPLE=noop
ENTRYPOINT [ "/extproc" ]
CMD [ "noop" ]
