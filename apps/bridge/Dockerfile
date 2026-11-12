# syntax=docker/dockerfile:1

# Begin a builder stage from base image
FROM golang:1.27-bookworm AS builder

# Set working directory
WORKDIR /src

# Download modules before copying sources so they stay cached
COPY go.* ./
RUN go mod download

# Build a static binary
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bridge .

# Begin a runner stage from a minimal base image that runs as a non-root user
FROM gcr.io/distroless/static-debian12:nonroot AS runner

# Copy the compiled binary
COPY --from=builder /out/bridge /usr/local/bin/bridge

# Launch the bridge
ENTRYPOINT ["bridge"]
