FROM golang:1.26.7-alpine AS builder

RUN apk add --no-cache git make

WORKDIR /workspace

COPY . ./

RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o benchctl ./cmd

# Stage 2: Runtime
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /app

COPY --from=builder /workspace/benchctl .

# Create non-root user
RUN adduser -D -u 1000 benchctl
USER benchctl

ENTRYPOINT ["/app/benchctl"]
CMD ["--help"]
