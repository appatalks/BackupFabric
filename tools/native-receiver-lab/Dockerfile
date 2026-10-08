FROM ubuntu:24.04

ARG DEBIAN_FRONTEND=noninteractive
ARG UID=10001
ARG GID=10001
RUN apt-get update \
    && apt-get install -y --no-install-recommends bash rsync util-linux coreutils \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid "${GID}" receiver \
    && useradd --uid "${UID}" --gid "${GID}" --no-create-home --shell /bin/bash admin \
    && mkdir -p /data/backup

COPY ghe-version /usr/local/bin/ghe-version
RUN chmod 0755 /usr/local/bin/ghe-version
USER ${UID}:${GID}
WORKDIR /data/backup
CMD ["/bin/cat"]