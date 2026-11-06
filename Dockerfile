# Build
FROM golang:1.24-alpine AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# Runtime
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata \
  && adduser -D -H -u 10001 app
WORKDIR /app
COPY --from=build /out/server /app/server
COPY web /app/web
COPY data /app/data
USER app
ENV PORT=8080 \
    WEB_DIR=/app/web \
    DICT_PATH=/app/data/spa-deu.json \
    CONJ_PATH=/app/data/spa-conj.json \
    APP_ROOT=/app
EXPOSE 8080
CMD ["/app/server"]
