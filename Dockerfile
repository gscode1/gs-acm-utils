# syntax=docker/dockerfile:1.7

# Builder Go version must match go.mod.
FROM golang:1.26-alpine AS build

# Optional private Go proxy. Non-secret coordinates only; the credential is
# NEVER passed as ARG/ENV, only as the build secret "goproxy_credential"
# (a file containing "user:token"). Without GOPROXY_HOST the default public
# proxy is used and the secret is ignored.
ARG GOPROXY_HOST=""
ARG GOPROXY_PATH=""
ARG VERSION=dev

ENV CGO_ENABLED=0
WORKDIR /src

COPY go.mod go.sum ./

# The credential exists only inside this RUN (secret mounts are tmpfs, never
# written to a layer) and GOPROXY with credentials is only exported here.
RUN --mount=type=secret,id=goproxy_credential,required=false \
    sh -c 'if [ -n "${GOPROXY_HOST}" ] && [ -s /run/secrets/goproxy_credential ]; then \
             export GOPROXY="https://$(tr -d "\n" < /run/secrets/goproxy_credential)@${GOPROXY_HOST}${GOPROXY_PATH}"; \
           fi; \
           go mod download'

COPY . .

# All modules are in the module cache of the previous layer; no network needed.
RUN GOPROXY=off go build -trimpath \
      -ldflags "-s -w -X github.com/gscode1/gs-acm-utils/internal/cli.version=${VERSION}" \
      -o /out/acm-mock-resolver ./cmd/acm-mock-resolver

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/acm-mock-resolver /usr/local/bin/acm-mock-resolver
ENTRYPOINT ["acm-mock-resolver"]
