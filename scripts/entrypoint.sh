#!/bin/bash
set -e

echo "[entrypoint] starting rsyslog..."
rm -f /var/run/rsyslogd.pid
touch /var/log/syslog
rsyslogd
tail -f /var/log/syslog &

echo "[entrypoint] running setup..."
/usr/local/bin/mailio setup

echo "[entrypoint] starting opendkim..."
opendkim -f -x /etc/opendkim.conf &
OPENDKIM_PID=$!

echo "[entrypoint] starting opendmarc..."
opendmarc -f -c /etc/opendmarc.conf &
OPENDMARC_PID=$!

# wait for opendkim (8891) and opendmarc (8893) to bind before postfix connects
sleep 1

echo "[entrypoint] starting postfix..."
postfix start-fg &
POSTFIX_PID=$!

# Background certificate renewal loop. Exits immediately if Let's Encrypt is not
# configured; otherwise checks daily and reloads postfix when the cert renews.
echo "[entrypoint] starting cert renewal loop..."
/usr/local/bin/mailio cert renew-loop &
RENEW_PID=$!

_term() {
    echo "[entrypoint] caught signal, shutting down..."
    postfix stop 2>/dev/null || true
    kill "$OPENDKIM_PID" 2>/dev/null || true
    kill "$OPENDMARC_PID" 2>/dev/null || true
    kill "$RENEW_PID" 2>/dev/null || true
    pkill rsyslogd 2>/dev/null || true
}
trap _term TERM INT

wait -n "$OPENDKIM_PID" "$OPENDMARC_PID" "$POSTFIX_PID" 2>/dev/null || true
echo "[entrypoint] process exited"
postfix stop 2>/dev/null || true
kill "$OPENDKIM_PID" 2>/dev/null || true
kill "$OPENDMARC_PID" 2>/dev/null || true
kill "$RENEW_PID" 2>/dev/null || true
pkill rsyslogd 2>/dev/null || true
