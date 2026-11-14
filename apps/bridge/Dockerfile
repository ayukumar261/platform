# syntax=docker/dockerfile:1

# Begin a builder stage from base image
FROM golang:1.27-trixie AS builder

# Install the C libraries for encoding Opus and resampling audio
RUN apt-get update \
 && apt-get install --yes --no-install-recommends libopus-dev libopusfile-dev libsoxr-dev pkg-config \
 && rm -rf /var/lib/apt/lists/*

# Set working directory
WORKDIR /src

# Download modules before copying sources so they stay cached
COPY go.* ./
RUN go mod download

# Build a binary that links the audio libraries
COPY . .
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/bridge .

# Begin a runner stage from the same Debian release as the builder
FROM debian:trixie-slim AS runner

# Install the audio libraries, and the certificates for connecting to Cartesia
RUN apt-get update \
 && apt-get install --yes --no-install-recommends libopus0 libopusfile0 libsoxr0 ca-certificates \
 && rm -rf /var/lib/apt/lists/*

# Run as a non-root user
USER nobody:nogroup

# Copy the compiled binary
COPY --from=builder /out/bridge /usr/local/bin/bridge

# Launch the bridge
ENTRYPOINT ["bridge"]
