FROM quay.io/app-sre/golang:1.17.11 as builder
WORKDIR /app
COPY . .
RUN go build -o /usr/bin/obsctl-reloader

FROM registry.access.redhat.com/ubi8/ubi-minimal:8.4
COPY --chown=0:0 --from=builder /usr/bin/obsctl-reloader /usr/local/bin/

CMD ["obsctl-reloader"]
