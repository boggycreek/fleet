# Copyright (c) 2026 Boggy Creek Software LLC
#
# Use of this source code is governed by an MIT-style
# license that can be found in the LICENSE file.

# Zero-leakage OpenRouter inference proxy container (ADR 00052)
FROM docker.io/library/golang:alpine AS builder

WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /bin/fleet-openrouter ./sandbox/cmd/fleet-openrouter

FROM docker.io/library/alpine:3.20

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -g 1000 openrouter && \
    adduser -u 1000 -G openrouter -s /bin/sh -D openrouter

COPY --from=builder /bin/fleet-openrouter /usr/local/bin/fleet-openrouter

USER openrouter
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/fleet-openrouter"]
