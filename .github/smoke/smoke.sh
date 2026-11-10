#!/usr/bin/env bash
#
# Boots the built image as a deployment would and checks what only a running container can show:
# that it starts healthy with nothing but its environment and a volume, that the admin link gets
# somebody in, that mail delivered on 25 and sent on 465 can be read on 993, and that a restart
# over the volume keeps it all.
#
# Runs the same locally as in CI:
#   docker build -t mailio:smoke . && IMAGE=mailio:smoke .github/smoke/smoke.sh
set -euo pipefail

IMAGE=${IMAGE:?set IMAGE to the mailio image to test}
HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

NAME=mailio-smoke
VOLUME=mailio-smoke-data
export SMOKE_HTTP=${SMOKE_HTTP:-18080} SMOKE_SMTP=${SMOKE_SMTP:-12525} SMOKE_SUBMISSIONS=${SMOKE_SUBMISSIONS:-12465}
export SMOKE_SUBMISSION=${SMOKE_SUBMISSION:-12587} SMOKE_IMAP=${SMOKE_IMAP:-12993}

say() { printf '\n=== %s ===\n' "$1"; }
die() { printf 'FAIL: %s\n' "$1" >&2; exit 1; }
# `producer | grep -q` is a trap under pipefail: grep exits on the first match, the producer takes
# SIGPIPE, and the pipeline reports failure on a successful match. Match captured output instead.
logs() { docker logs "$NAME" 2>&1; }

cleanup() {
  status=$?
  if [ "$status" -ne 0 ]; then
    say "mailio log"; logs | tail -80 || true
  fi
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  docker volume rm "$VOLUME" >/dev/null 2>&1 || true
}
trap cleanup EXIT

# A previous interrupted run must not decide this one's result: a stale volume would make the
# first boot look like a restart.
docker rm -f "$NAME" >/dev/null 2>&1 || true
docker volume rm "$VOLUME" >/dev/null 2>&1 || true

healthy() {
  for _ in $(seq 60); do
    case "$(docker inspect -f '{{.State.Health.Status}}' "$NAME" 2>/dev/null || true)" in
      healthy) return 0 ;;
      unhealthy) die "the container reports unhealthy" ;;
    esac
    sleep 1
  done
  die "the container never became healthy"
}

say "refuses to start without a data volume"
if docker run --rm -e MAILIO_PUBLIC_URL=http://localhost -e "MAILIO_SECRET_KEY=$(openssl rand -base64 32)" \
  "$IMAGE" serve >/dev/null 2>&1; then
  die "it started with no /data mounted"
fi

say "starts with its environment and a volume"
docker run -d --name "$NAME" \
  -e "MAILIO_PUBLIC_URL=http://localhost:$SMOKE_HTTP" \
  -e MAILIO_MAIL_HOST=mail.smoke.test \
  -e "MAILIO_SECRET_KEY=$(openssl rand -base64 32)" \
  -v "$VOLUME:/data" \
  -p "127.0.0.1:$SMOKE_HTTP:80" -p "127.0.0.1:$SMOKE_SMTP:25" -p "127.0.0.1:$SMOKE_SUBMISSION:587" \
  -p "127.0.0.1:$SMOKE_SUBMISSIONS:465" -p "127.0.0.1:$SMOKE_IMAP:993" \
  "$IMAGE" >/dev/null
healthy

say "logs an admin link"
token=$(logs | sed -n 's/.*"link":"[^"]*#token=\([^"]*\)".*/\1/p' | tail -1)
[ -n "$token" ] || die "no admin link in the log"

say "admin, domain, account, mail in and out"
SMOKE_TOKEN=$token python3 "$HERE/smoke.py" setup

say "the log names no message"
if grep -qF "Smoke subject" <<<"$(logs)"; then die "a subject is in the log"; fi

say "a restart keeps the mail"
docker restart "$NAME" >/dev/null
healthy
python3 "$HERE/smoke.py" after-restart

say "version"
docker exec "$NAME" mailio version

say "ok"
