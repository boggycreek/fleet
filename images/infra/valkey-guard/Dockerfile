# Copyright (c) 2026 Boggy Creek Software LLC
# Use of this source code is governed by an MIT-style license that can be found in the LICENSE file.

FROM docker.io/library/golang:1.24-alpine AS builder

WORKDIR /app
COPY . .

RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /bin/valkey-guard ./backplane/cmd/valkey-guard

FROM docker.io/library/alpine:latest

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /bin/valkey-guard /usr/local/bin/valkey-guard

EXPOSE 6379

ENTRYPOINT ["/usr/local/bin/valkey-guard"]
