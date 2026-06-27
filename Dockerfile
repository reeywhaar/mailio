FROM golang:1.26-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY cli/ ./cli/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 go build -o /mailio ./cmd/mailio

FROM alpine:latest

# Pin the opendmarc user to a fixed high UID *before* installing packages.
# Otherwise the opendmarc package's post-install grabs the next free system UID
# (101), which displaces the postfix user to 102. Because /var/spool/postfix is
# a persisted volume owned by the original postfix UID (101), that drift makes
# the queue unreadable on the next start — postsuper fails with "scan_dir_push:
# open directory defer: Permission denied" and the integrity check aborts boot.
# Creating opendmarc up front (UID 900, primary group "mail" as the package
# would) keeps postfix at 101/gid 102, so the existing queue stays accessible.
RUN adduser -S -D -H -h /run/opendmarc -s /sbin/nologin -G mail -u 900 opendmarc

RUN apk add --no-cache \
    postfix \
    postfix-pcre \
    cyrus-sasl \
    cyrus-sasl-login \
    cyrus-sasl-crammd5 \
    opendkim \
    opendkim-utils \
    opendmarc \
    bash \
    rsyslog \
    busybox-extras \
    openssl

# Minimal rsyslog config: receive from Unix socket, forward everything to stdout
RUN printf 'module(load="imuxsock")\n*.* /var/log/syslog\n' > /etc/rsyslog.conf

RUN id vmail 2>/dev/null || adduser -D -s /sbin/nologin vmail

COPY --from=builder /mailio /usr/local/bin/mailio
COPY scripts/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

EXPOSE 25 587

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
