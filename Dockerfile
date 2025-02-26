FROM registry.ci.openshift.org/ocp/builder:rhel-8-golang-1.19-openshift-4.12 AS builder
WORKDIR /app
COPY . .
RUN go build -mod=readonly -o /tmp/obsctl-reloader

FROM registry.access.redhat.com/ubi8/ubi-minimal:8.10-1179.1739286367
COPY --chown=0:0 --from=builder /tmp/obsctl-reloader /usr/local/bin/

# level=error msg="add api" error="creating config directory: mkdir /.config: permission denied"
ENV OBSCTL_CONFIG_PATH=/tmp/obsctl

ENTRYPOINT [ "obsctl-reloader" ]
