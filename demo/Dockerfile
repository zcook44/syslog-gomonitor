# Disposable demo. The monitor binary serves the dashboard itself.
FROM golang:1.24-bookworm AS build

WORKDIR /src
COPY . .
RUN go build -trimpath -o /out/syslog-gomonitor ./cmd/syslog-gomonitor

FROM rockylinux:9

RUN dnf -y install iproute \
    && dnf clean all \
    && mkdir -p /var/lib/syslog-gomonitor-demo

COPY --from=build /out/syslog-gomonitor /usr/local/bin/syslog-gomonitor

EXPOSE 8080

CMD ["syslog-gomonitor", "demo", "--listen", "0.0.0.0:8080", "--root", "/var/lib/syslog-gomonitor-demo"]
