# RTPEngine

We build our own image using Debian 13 and RTPEngine `12.5.1.31-1`. It runs without root access and forwards media without a kernel module.

## Start

Run from the repository root:

```sh
docker compose up -d --build --wait rtpengine
```

## Check

The health check asks RTPEngine for its session count. You can also run:

```sh
docker compose exec rtpengine rtpengine-ctl list numsessions
```

## Ports

| Purpose | Local address | Protocol |
| --- | --- | --- |
| Control API | `127.0.0.1:2223` | UDP |
| Media | `127.0.0.1:30000-30099` | UDP |

Other Compose services can reach the control API at `rtpengine:2223` over UDP.

## Configuration

Settings live in [rtpengine.conf](rtpengine.conf). `eth0!127.0.0.1` means: listen on the container's network interface and tell clients to send media to localhost.

For a VPS, use its reachable media IP and publish the media ports on the correct host interface. Keep the control API private. The configured media port range must match the published range.

Kamailio routing still needs to be connected to RTPEngine and the SIP trunk.
