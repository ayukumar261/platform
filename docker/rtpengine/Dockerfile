FROM debian:trixie-slim

RUN apt-get update

RUN apt-get install --yes --no-install-recommends rtpengine-daemon=12.5.1.31-1

RUN apt-get install --yes --no-install-recommends rtpengine-utils=12.5.1.31-1

RUN rm -rf /var/lib/apt/lists/*

COPY rtpengine.conf /etc/rtpengine/rtpengine.conf

USER nobody:nogroup

ENTRYPOINT ["rtpengine"]
