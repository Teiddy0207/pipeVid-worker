# Dockerfile — view-sync worker (Redis → PostgreSQL batch sync).
# Build context: worker/
# Build: docker build -t pipevid-view-sync .

# ── Stage 1: Go modules ───────────────────────────────────────────────────────
FROM golang:1.25-alpine AS modules
COPY go.mod go.sum /modules/
WORKDIR /modules
RUN go mod download

# ── Stage 2: Builder ─────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder
COPY --from=modules /go/pkg /go/pkg
COPY . /src
WORKDIR /src
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /bin/view-sync .

# ── Stage 3: Runtime ─────────────────────────────────────────────────────────
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
ENV TZ=Asia/Ho_Chi_Minh
COPY --from=builder /bin/view-sync /app/view-sync
CMD ["/app/view-sync"]
