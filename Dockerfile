FROM golang:1.27-alpine AS builder

# Stamped into the binary so a pulled image can say which commit it is. "dev" is
# what a local build says, and it is true.
ARG VERSION=dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 go build -ldflags "-X mailio/internal/app.Version=$VERSION" -o /mailio ./cmd/mailio

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

# All three daemons, not just Postfix: milter_default_action=accept means a dead
# OpenDKIM is invisible from outside — mail keeps flowing, unsigned, until
# recipients start filing it as spam.
#
# start-period covers the first boot, where DKIM keygen, an ACME order and the
# DNS round trips all happen before Postfix binds.
HEALTHCHECK --interval=30s --timeout=10s --start-period=120s --retries=3 \
    CMD ["/usr/local/bin/mailio", "healthcheck"]

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
