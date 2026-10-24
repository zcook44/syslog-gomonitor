FROM alpine:3.20

RUN apk add --no-cache netcat-openbsd
COPY send.sh /send.sh
RUN chmod 0755 /send.sh

ENTRYPOINT ["/send.sh"]
