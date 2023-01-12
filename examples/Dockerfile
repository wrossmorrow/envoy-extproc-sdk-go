FROM golang:1.19.2-bullseye

SHELL ["/bin/bash", "-c"]

RUN apt-get update && apt-get -y upgrade \
    && apt-get autoremove -y \
    && rm -rf /var/lib/apt/lists/* \
    && apt-get -y clean

# https://github.com/grpc-ecosystem/grpc-health-probe/#example-grpc-health-checking-on-kubernetes
RUN GRPC_HEALTH_PROBE_VER=v0.3.1 \
    && GRPC_HEALTH_PROBE_URL=https://github.com/grpc-ecosystem/grpc-health-probe/releases/download/${GRPC_HEALTH_PROBE_VER}/grpc_health_probe-linux-amd64 \
    && curl ${GRPC_HEALTH_PROBE_URL} -L -s -o /bin/grpc_health_probe \
    && chmod +x /bin/grpc_health_probe

WORKDIR /app

ARG EXAMPLE=noop

COPY ${EXAMPLE}/go.mod ${EXAMPLE}/*.go  ./
RUN sed -Ei.bak '/replace github\.com\/wrossmorrow\/envoy-extproc-sdk-go/d' go.mod \
    && go mod tidy \
    && go mod download \
    && go build -o /extproc

EXPOSE 50051

CMD [ "/extproc" ]


