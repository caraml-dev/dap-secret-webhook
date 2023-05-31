FROM golang:1.20-alpine AS build
ENV GO111MODULE on
ENV CGO_ENABLED 0

ADD . /app/

WORKDIR /app

RUN go build -tags musl -o dap-secret-webhook cmd/main.go

ENTRYPOINT ["sh", "-c", "/app/dap-secret-webhook \"$@\"", "--"]