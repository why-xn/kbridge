# syntax=docker/dockerfile:1
# Multi-stage build for the kbridge central service.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
RUN CGO_ENABLED=0 GOOS=linux go build \
  -ldflags="-s -w -X github.com/why-xn/kbridge/internal/version.Version=${VERSION} -X github.com/why-xn/kbridge/internal/version.Commit=${COMMIT} -X github.com/why-xn/kbridge/internal/version.Date=${DATE}" \
  -o /out/kbridge-central ./cmd/central

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 kbridge
COPY --from=build /out/kbridge-central /usr/local/bin/kbridge-central
USER kbridge
EXPOSE 8080 9090
ENTRYPOINT ["kbridge-central"]
CMD ["--config", "/etc/kbridge/central.yaml"]
