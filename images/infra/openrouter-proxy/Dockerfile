# Copyright (c) 2026 Boggy Creek Software LLC
#
# Use of this source code is governed by an MIT-style
# license that can be found in the LICENSE file.

FROM docker.io/library/golang:1.24-alpine AS builder

WORKDIR /app
COPY . .

RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /bin/fleet-openrouter-proxy ./sandbox/cmd/fleet-openrouter-proxy

FROM docker.io/library/alpine:latest

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /bin/fleet-openrouter-proxy /usr/local/bin/fleet-openrouter-proxy

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/fleet-openrouter-proxy"]
