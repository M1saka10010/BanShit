ARG GO_IMAGE=golang:1.26-alpine
ARG RUNTIME_IMAGE=alpine:3.23
FROM ${GO_IMAGE} AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOMAXPROCS=2 GOGC=30
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
COPY internal/ ./internal/
COPY public/ ./public/
RUN go build -p 1 -trimpath -ldflags="-s -w" -o /out/banshit .

FROM ${RUNTIME_IMAGE}
RUN apk add --no-cache ca-certificates \
    && addgroup -g 10001 banshit \
    && adduser -D -H -u 10001 -G banshit banshit \
    && mkdir -p /app /config /data \
    && chown 10001:10001 /config /data
COPY --from=build /out/banshit /app/banshit
COPY LICENSE /usr/share/licenses/banshit/LICENSE
WORKDIR /app
ENV BANSHIT_CONFIG=/config/config.json BANSHIT_DATA=/data
USER 10001:10001
EXPOSE 3000
ENTRYPOINT ["/app/banshit"]
