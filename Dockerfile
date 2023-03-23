FROM registry.access.redhat.com/ubi8/go-toolset:1.18.9-13 as builder
WORKDIR /app
COPY . .
RUN go build -o /tmp/obsctl-reloader

FROM registry.access.redhat.com/ubi8/ubi-minimal:8.4
COPY --chown=0:0 --from=builder /tmp/obsctl-reloader /usr/local/bin/

# level=error msg="add api" error="creating config directory: mkdir /.config: permission denied"
ENV OBSCTL_CONFIG_PATH=/tmp/obsctl

ENTRYPOINT [ "obsctl-reloader" ]
