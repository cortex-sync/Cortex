# Bakes the freshly generated self-signed cert into the Gitea image. A bind
# mount of ./certs works locally but NOT under docker-in-docker (CI), where the
# mount would reference the daemon's filesystem, not the job's. The build
# context (this dir) is shipped to the daemon, so COPY sees the certs either way.
# Throwaway, localhost/CI only - never reuse this image or cert.
# Pinned by digest: the e2e job gates releases, so a re-pushed mutable tag must
# not be able to change release-blocking behaviour. Resolved from gitea/gitea:1.26.
FROM gitea/gitea:28.2@sha256:30777c91280d973fa02be48b1ade735bd6859e8f6f7af3b4444d519437852504
COPY certs/ /certs/
# /certs must be traversable (0755) and the files readable by the unprivileged
# gitea user; COPY alone can leave the dir without its execute bit.
RUN chmod 0755 /certs && chmod 0644 /certs/server.crt /certs/server.key
