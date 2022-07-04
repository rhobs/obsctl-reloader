FROM quay.io/openshift/origin-cli:4.10.0 as oc

FROM quay.io/app-sre/golang:1.17.11 as obsctl
RUN go install github.com/observatorium/obsctl@latest

FROM registry.access.redhat.com/ubi8/python-39:1-51
COPY --chown=0:0 --from=oc /usr/bin/oc /usr/local/bin/
COPY --chown=0:0 --from=obsctl /go/bin/obsctl /usr/local/bin/
COPY run.py .
