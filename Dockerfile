FROM golang:1.23-alpine

WORKDIR /cmp

COPY go.mod go.sum ./

RUN go mod download

COPY ./cmd/yokecd ./cmd/yokecd
COPY ./internal ./internal
COPY ./pkg ./pkg

RUN go install ./cmd/yokecd

COPY ./cmd/yokecd/plugin.yaml /home/argocd/cmp-server/config/plugin.yaml

RUN chmod -R 777 /go && mkdir /.cache && chmod -R 777 /.cache


