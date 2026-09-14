#!/usr/bin/env bash
#
# Boots the built image against a fake DNS API and checks the things that only
# a running container can show: that setup publishes the right DNS records and
# then leaves them alone, that an untrusted client cannot relay, that
# authenticated mail is signed and delivered, that account changes apply without
# a restart, and that a restart over the persisted volumes comes back up.
#
# Runs the same locally as in CI:
#   docker build -t mailio:smoke . && IMAGE=mailio:smoke .github/smoke/smoke.sh
set -euo pipefail

IMAGE=${IMAGE:?set IMAGE to the mailio image to test}
HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

NET=mailio-smoke-net
MAILIO=mailio-smoke
DNSAPI=mailio-smoke-dnsapi
VOLUMES="mailio-smoke-keys mailio-smoke-spool mailio-smoke-mail mailio-smoke-tls"

export SMOKE_HOST=127.0.0.1
export SMOKE_SMTP_PORT=${SMOKE_SMTP_PORT:-2525}
export SMOKE_SUBMISSION_PORT=${SMOKE_SUBMISSION_PORT:-2587}
DNS_PORT=${DNS_PORT:-8433}

WORK=$(mktemp -d)
check() { python3 "$HERE/mailcheck.py" "$@"; }
# `producer | grep -q` is a trap under pipefail: grep exits on the first match,
# the producer takes SIGPIPE, and the pipeline reports failure on a *successful*
# match. Everything below matches against captured output instead.
log_has() { grep -qF "$1" <<<"$(docker logs "$MAILIO" 2>&1)"; }
say() { printf '\n=== %s ===\n' "$1"; }
die() { printf 'FAIL: %s\n' "$1" >&2; exit 1; }

cleanup() {
  status=$?
  if [ "$status" -ne 0 ]; then
    say "mailio log"; docker logs "$MAILIO" 2>&1 | tail -80 || true
    say "dnsapi log"; docker logs "$DNSAPI" 2>&1 | tail -40 || true
  fi
  docker rm -f "$MAILIO" "$DNSAPI" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
  # shellcheck disable=SC2086
  docker volume rm $VOLUMES >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

# A previous interrupted run must not decide this one's result: stale volumes
# would make the first boot look like a restart.
docker rm -f "$MAILIO" "$DNSAPI" >/dev/null 2>&1 || true
docker network rm "$NET" >/dev/null 2>&1 || true
# shellcheck disable=SC2086
docker volume rm $VOLUMES >/dev/null 2>&1 || true

# account add edits the config in place, so it runs against a copy.
cp "$HERE/config.yml" "$WORK/config.yml"
chmod 666 "$WORK/config.yml"

docker network create "$NET" >/dev/null

say "starting the fake DNS API"
# The seeded record is a DKIM key that is no longer the one mailio holds — the
# case where the upsert has to delete before it creates, rather than leaving two
# TXT records at the same name and a domain that signs mail nobody can verify.
docker run -d --name "$DNSAPI" --network "$NET" \
  -v "$HERE/dnsapi.py:/dnsapi.py:ro" \
  -e DOMAINS=smoke.test \
  -e 'SEED=[["smoke.test","TXT","mail._domainkey","v=DKIM1; k=rsa; p=STALEKEYFROMANOLDRUN"]]' \
  -p "127.0.0.1:$DNS_PORT:8433" \
  python:3-alpine python /dnsapi.py >/dev/null

for _ in $(seq 30); do
  if curl -fsS "http://127.0.0.1:$DNS_PORT/dns" >/dev/null 2>&1; then break; fi
  sleep 1
done
curl -fsS "http://127.0.0.1:$DNS_PORT/dns" >/dev/null || die "the fake DNS API never came up"

start_mailio() {
  docker run -d --name "$MAILIO" --network "$NET" --hostname mail.smoke.test \
    -v "$WORK/config.yml:/etc/mailio/config.yml" \
    -v mailio-smoke-keys:/etc/opendkim/keys \
    -v mailio-smoke-spool:/var/spool/postfix \
    -v mailio-smoke-mail:/var/mail \
    -v mailio-smoke-tls:/etc/postfix/tls \
    -e DNS_API_URL="http://$DNSAPI:8433" \
    -p "127.0.0.1:$SMOKE_SMTP_PORT:25" \
    -p "127.0.0.1:$SMOKE_SUBMISSION_PORT:587" \
    "$IMAGE" >/dev/null
}

wait_for_mailio() {
  for _ in $(seq 90); do
    if log_has '[entrypoint] starting postfix' \
      && (exec 3<>"/dev/tcp/127.0.0.1/$SMOKE_SUBMISSION_PORT") 2>/dev/null; then
      return 0
    fi
    [ "$(docker inspect -f '{{.State.Running}}' "$MAILIO" 2>/dev/null)" = true ] \
      || die "the container exited during startup"
    sleep 1
  done
  die "submission never started accepting connections"
}

say "first boot"
start_mailio
wait_for_mailio
log_has '[setup] done' || die "setup did not finish"

say "published DNS records"
dns() { curl -fsS "http://127.0.0.1:$DNS_PORT/dns"; }
ops() { curl -fsS "http://127.0.0.1:$DNS_PORT/_ops"; }

# jq is on the runner; -e makes an empty result a failure.
dns | jq -e '.[0].records[] | select(.name=="mail._domainkey") | select(.data|startswith("v=DKIM1"))' >/dev/null \
  || die "no DKIM record was published"
dns | jq -e '[.[0].records[] | select(.name=="mail._domainkey")] | length == 1' >/dev/null \
  || die "the stale DKIM record was left beside the new one"
if dns | jq -e '.[0].records[] | select(.data|contains("STALEKEY"))' >/dev/null; then
  die "the stale DKIM record was never deleted"
fi
dns | jq -e '.[0].records[] | select(.name=="@") | select(.data=="v=spf1 a:mail.smoke.test ~all")' >/dev/null \
  || die "no SPF record was published"
dns | jq -e '.[0].records[] | select(.name=="_dmarc") | select(.data=="v=DMARC1; p=none; rua=mailto:inbox@smoke.test")' >/dev/null \
  || die "no DMARC record was published"
echo "ok: DKIM, SPF and DMARC published; the stale DKIM record was replaced"

FIRST_OPS=$(ops | jq -c .)
echo "ok: DNS writes on first boot: $FIRST_OPS"

say "the credential commands read the running config"
creds=$(docker exec "$MAILIO" mailio config show)
grep -qF 'alice@smoke.test' <<<"$creds" || die "mailio config does not list alice"
grep -qF 'alice-smoke-pw' <<<"$creds" || die "mailio config does not print the password"
grep -qF 'set mailserver localhost port 587' <<<"$(docker exec "$MAILIO" mailio config monit)" \
  || die "mailio config monit did not render"
grep -qF 'smtp://alice%40smoke.test@mail.smoke.test:587/' <<<"$(docker exec "$MAILIO" mailio config mutt)" \
  || die "mailio config mutt did not render"
jq -e '.accounts[] | select(.address=="alice@smoke.test") | select(.password=="alice-smoke-pw")' >/dev/null \
  <<<"$(docker exec "$MAILIO" mailio config show --json)" || die "config show --json did not render"
echo "ok: config show, monit, mutt and --json all render"

say "the account listing"
accounts=$(docker exec "$MAILIO" mailio account list)
grep -qF '/var/mail/alice' <<<"$accounts" || die "account list does not show alice's mailbox"
grep -qE 'noreply@smoke.test +send-only' <<<"$accounts" || die "account list does not mark noreply send-only"
json=$(docker exec "$MAILIO" mailio account list --json)
jq -e '.[] | select(.address=="noreply@smoke.test") | select(.send_only==true)' >/dev/null <<<"$json" \
  || die "account list --json does not mark noreply send-only"
jq -e '.[] | select(.address=="alice@smoke.test") | select(.send_only==false)' >/dev/null <<<"$json" \
  || die "account list --json marks alice send-only"
echo "ok: account list names every mailbox and every send-only account"

say "the command line refuses what it does not understand"
# This is what the cobra rewrite is for. The old hand-rolled dispatch fell
# through to setup() on anything it did not recognise, so a typo rewrote the
# Postfix configuration and republished DNS records. Both halves are asserted:
# a non-zero exit, and a DNS write log that did not move.
before_ops=$(ops | jq -c .)
for bad in "add-acount smoke.test dave" "accounts list" "--nonsense" "cert bogus" "account add onlyonearg"; do
  # shellcheck disable=SC2086
  if docker exec "$MAILIO" mailio $bad >/dev/null 2>&1; then
    die "\`mailio $bad\` was accepted"
  fi
done
[ "$(ops | jq -c .)" = "$before_ops" ] || die "a refused command still wrote to DNS"
# A bare command group lists what it offers rather than erroring, and — the
# point — rather than doing anything.
grep -qF 'Available Commands:' <<<"$(docker exec "$MAILIO" mailio)" || die "bare mailio did not print help"
grep -qF 'renew' <<<"$(docker exec "$MAILIO" mailio cert)" || die "bare cert did not list its subcommands"
[ -n "$(docker exec "$MAILIO" mailio version)" ] || die "mailio version printed nothing"
echo "ok: unknown commands are refused, and refusing them changes nothing"

say "healthcheck"
docker exec "$MAILIO" mailio healthcheck || die "healthcheck failed on a healthy container"
echo "ok: healthcheck passes while all three daemons are up"

say "an untrusted client cannot relay"
# Senders live under .test, which is reserved and never resolves, so no check
# here turns on what some real domain happens to publish today. (example.net
# publishes DMARC p=reject, which would have this suite failing on somebody
# else's zone file.)
# The single check that matters most: published on the internet, an open relay
# is found within hours. The host connects through a published port, which
# postfix sees as the bridge gateway — not in mynetworks — so this is exactly
# what a stranger gets.
check inbound-refused nobody@outside.test someone@elsewhere.test --expect "Relay access denied"
check inbound-refused nobody@outside.test nosuchuser@smoke.test --expect "User unknown"

say "the DMARC milter is in the chain"
# Nothing else in this suite would notice if opendmarc were unreachable:
# milter_default_action=accept means a dead milter fails open and every message
# sails through. A message missing a mandatory RFC5322 header is refused only if
# opendmarc is actually running and connected, so this is the check that it is.
check inbound-no-date sender@outside.test alice@smoke.test

say "inbound mail for a real mailbox is accepted"
check inbound sender@outside.test alice@smoke.test inbound-1

say "submission"
check auth-refused alice@smoke.test wrong-password
check auth alice@smoke.test alice-smoke-pw
# An account may only send as itself; without this, one leaked password lets the
# holder send as every address the server hosts.
check submit-refused alice@smoke.test alice-smoke-pw bob@smoke.test someone@elsewhere.test \
  --expect "not owned by user"
check submit alice@smoke.test alice-smoke-pw alice@smoke.test bob@smoke.test submitted-1

say "a send-only account can send but not receive"
check auth noreply@smoke.test noreply-smoke-pw
check submit noreply@smoke.test noreply-smoke-pw noreply@smoke.test bob@smoke.test sendonly-1
check inbound-refused nobody@outside.test noreply@smoke.test --expect "User unknown"

say "delivery"
delivered() { # <maildir> <marker>
  for _ in $(seq 30); do
    if docker exec "$MAILIO" sh -c "grep -rlF 'X-Smoke: $2' /var/mail/$1/new" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  docker exec "$MAILIO" sh -c "ls -la /var/mail/$1/new" || true
  die "$2 was never delivered to /var/mail/$1"
}
delivered alice inbound-1
delivered bob submitted-1
delivered bob sendonly-1
echo "ok: every submitted message reached its Maildir"

# Signing is the reason opendkim is in the image at all, and an unsigned message
# is accepted by the next hop right up until it is silently spam-foldered.
raw=$(docker exec "$MAILIO" sh -c "grep -rlF 'X-Smoke: submitted-1' /var/mail/bob/new | xargs cat")
# The signature is folded across lines; unfold before matching.
grep -q 'DKIM-Signature:.*d=smoke.test' <<<"$(tr -d '\r' <<<"$raw" | tr '\n' ' ')" \
  || die "the submitted message was not DKIM-signed for smoke.test"
echo "ok: submitted mail is DKIM-signed with d=smoke.test"

say "account changes apply without a restart"
docker exec "$MAILIO" mailio account add smoke.test carol --password carol-smoke-pw >/dev/null
check auth carol@smoke.test carol-smoke-pw
grep -q 'local_part: carol' "$WORK/config.yml" || die "account add did not write the config"
docker exec "$MAILIO" mailio account delete smoke.test carol >/dev/null
check auth-refused carol@smoke.test carol-smoke-pw
if grep -q 'local_part: carol' "$WORK/config.yml"; then die "account delete did not update the config"; fi
echo "ok: account add and account delete both took effect live"

say "the uids the persisted volumes depend on"
# The Dockerfile pins opendmarc to 900 so the package's post-install cannot take
# the low system uid and push postfix off the uid that owns the existing
# /var/spool/postfix. Getting this wrong only shows up on the *second* start of
# an installation, as an unreadable queue.
[ "$(docker exec "$MAILIO" id -u opendmarc)" = 900 ] || die "opendmarc is not uid 900"
POSTFIX_UID=$(docker exec "$MAILIO" id -u postfix)
[ "$POSTFIX_UID" -lt 900 ] || die "postfix landed above the pinned opendmarc uid"
docker exec "$MAILIO" sh -c 'test "$(stat -c %u /var/spool/postfix/defer)" = "$(id -u postfix)"' \
  || die "/var/spool/postfix/defer is not owned by the postfix user"
echo "ok: opendmarc is 900, postfix is $POSTFIX_UID and owns its queue"

say "restart over the persisted volumes"
docker rm -f "$MAILIO" >/dev/null
start_mailio
wait_for_mailio
if log_has 'Permission denied'; then
  die "the restarted container cannot read its persisted state"
fi
log_has '[tls] cert already exists' \
  || die "the restarted container re-issued its certificate instead of reusing it"

# Setup runs again on every start. Re-creating an already-correct record would
# churn the zone on every restart, and with it the DKIM key mail is signed
# against.
[ "$(ops | jq -c .)" = "$FIRST_OPS" ] || die "the second boot rewrote DNS records: $(ops | jq -c .)"
echo "ok: the second boot reused the cert and published nothing new"

check submit alice@smoke.test alice-smoke-pw alice@smoke.test bob@smoke.test submitted-2
delivered bob submitted-2
echo "ok: mail still flows after a restart"

# Last, because it kills a daemon the container does not come back from: the
# entrypoint waits on opendkim and shuts down when it exits.
say "the healthcheck notices a daemon that is gone"
docker exec "$MAILIO" pkill -f opendkim
if docker exec "$MAILIO" mailio healthcheck 2>/dev/null; then
  die "healthcheck passed with opendkim dead"
fi
echo "ok: healthcheck fails when a milter is down"

say "smoke test passed"
