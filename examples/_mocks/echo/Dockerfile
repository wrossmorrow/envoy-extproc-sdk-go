FROM golang:1.19.2-bullseye

SHELL ["/bin/bash", "-c"]

RUN apt-get update && apt-get -y upgrade \
    && apt-get autoremove -y \
    && rm -rf /var/lib/apt/lists/* \
    && apt-get -y clean

WORKDIR /app

COPY ./ ./
RUN go mod download && go build -o /echo
EXPOSE 8000
CMD [ "/echo" ]
