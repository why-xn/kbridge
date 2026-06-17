# syntax=docker/dockerfile:1
# Multi-stage build for the kbridge CLI.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/kbridge ./cmd/kbridge

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 kbridge
COPY --from=build /out/kbridge /usr/local/bin/kbridge
USER kbridge
ENTRYPOINT ["kbridge"]
