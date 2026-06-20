# syntax=docker/dockerfile:1
# Multi-stage build for the kbridge agent. The final image bundles kubectl,
# which the agent shells out to.
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
  -o /out/kbridge-agent ./cmd/agent

FROM alpine:3.20
ARG KUBECTL_VERSION=v1.31.0
ARG TARGETARCH=amd64
RUN apk add --no-cache ca-certificates curl \
    && curl -fsSLo /usr/local/bin/kubectl \
        "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl" \
    && chmod +x /usr/local/bin/kubectl \
    && apk del curl \
    && adduser -D -u 10001 kbridge
COPY --from=build /out/kbridge-agent /usr/local/bin/kbridge-agent
USER kbridge
ENTRYPOINT ["kbridge-agent"]
CMD ["--config", "/etc/kbridge/agent.yaml"]
