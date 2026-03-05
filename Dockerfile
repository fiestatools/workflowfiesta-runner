FROM golang:1.23-alpine AS builder
WORKDIR /build
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o workflowfiesta-runner ./cmd/runner

FROM alpine:3.20 AS runner
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /build/workflowfiesta-runner .
ENTRYPOINT ["./workflowfiesta-runner"]
CMD ["run"]
