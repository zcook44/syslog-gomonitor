# Collector image for the lab. Rebuild this when the monitor source changes.
FROM golang:1.24-bookworm AS build

WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/syslog-gomonitor ./cmd/syslog-gomonitor

FROM rockylinux:9

RUN dnf -y install rsyslog iproute gzip && dnf clean all \
    && mkdir -p /etc/syslog-gomonitor /var/log/remote /var/log/firewall /var/lib/syslog-gomonitor

COPY --from=build /out/syslog-gomonitor /usr/local/bin/syslog-gomonitor
COPY lab/collector/rsyslog.conf /etc/rsyslog.conf
COPY lab/collector/devices.json /etc/syslog-gomonitor/devices.json
COPY lab/collector/entrypoint.sh /entrypoint.sh
RUN chmod 0755 /entrypoint.sh /usr/local/bin/syslog-gomonitor

EXPOSE 514/tcp 514/udp 8080

ENTRYPOINT ["/entrypoint.sh"]
