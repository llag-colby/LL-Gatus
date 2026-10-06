#!/usr/bin/env bash
# One-shot WinRM setup for the hypervisor collector.
#
# Run this ONCE on the box that runs Gatus. It does everything on this side:
#   - works out this machine's IP as the hypervisors will see it
#   - creates the CA and the collector's client certificate
#   - creates a server certificate for every host in HOSTS below
#   - writes ONE self-contained PowerShell script, gatus-winrm-setup.ps1, with
#     the CA, every server certificate and this machine's IP already embedded
#
# Then there is exactly one thing left to do: copy that one file to each
# hypervisor and run it elevated. It takes no arguments and picks its own
# certificate by hostname.
#
# Re-runnable. Pass --force to regenerate the CA (which means re-running the
# PowerShell script on every host, because the trusted issuer changes).
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
CERTS="$HERE/winrm-certs"
OUT="$HERE/gatus-winrm-setup.ps1"
ACCOUNT="svc-gatus-ro"
UPN="${ACCOUNT}@localhost"
DAYS=1095
FORCE=0
[ "${1:-}" = "--force" ] && FORCE=1

# Host inventory. Keep in step with HOSTS in hv_collector.py.
# Format: shortname|fqdn|ip[,ip...]
HOSTS='
CU-HV01|CU-HV01.longlewis.local|10.28.106.231
FL-HV01|FL-HV01.longlewis.local|10.8.102.172,10.8.102.197
HOOV-HV01|HOOV-HV01.longlewis.local|10.16.102.6,10.16.102.7
HOOV-HV02|HOOV-HV02.longlewis.local|10.16.102.122
MS-HV01|MS-HV01.longlewis.local|10.6.108.125,10.6.80.105
MS-HV02|MS-HV02.longlewis.local|10.6.80.1,10.6.80.44
MS-HV03|MS-HV03.longlewis.local|10.6.80.85
MS-HV04|MS-HV04.longlewis.local|10.6.108.131,10.6.80.99
MS-HV06|MS-HV06.longlewis.local|10.6.108.133,10.6.80.12
ONA-HV1|ONA-HV1.longlewis.local|10.15.101.230,10.15.102.25
ONA-HV2|ONA-HV2.longlewis.local|10.15.102.161
'

say()  { printf '%s\n' "$*"; }
step() { printf '\n== %s\n' "$*"; }
die()  { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

# --- openssl, natively or in a container ------------------------------------
if command -v openssl >/dev/null 2>&1; then
  ssl() { openssl "$@"; }
elif command -v docker >/dev/null 2>&1; then
  say "openssl not installed locally, using a container for it"
  ssl() { docker run --rm -v "$CERTS:/w" -w /w alpine/openssl:latest "$@"; }
else
  die "need either openssl or docker"
fi

# --- 1. this machine's IP, as the hypervisors see it ------------------------
step "Working out this machine's address"
PROBE_IP="$(printf '%s' "$HOSTS" | sed -n '5p' | cut -d'|' -f3 | cut -d',' -f1)"

# An explicit value always wins, because only you know whether this box or
# another one is going to do the collecting.
DETECTED=""
if [ -z "${COLLECTOR_IP:-}" ]; then
  # `ip route get` is the right answer: it reports the source address the
  # kernel would actually use for that destination, which is what the
  # hypervisor sees. Containers SNAT to it.
  if command -v ip >/dev/null 2>&1; then
    DETECTED="$(ip route get "$PROBE_IP" 2>/dev/null | sed -n 's/.*src \([0-9.]*\).*/\1/p' | head -1 || true)"
  fi
  # Fallbacks for boxes without iproute2.
  if [ -z "$DETECTED" ] && command -v hostname >/dev/null 2>&1; then
    DETECTED="$(hostname -I 2>/dev/null | awk '{print $1}' || true)"
  fi
  if [ -z "$DETECTED" ] && command -v ifconfig >/dev/null 2>&1; then
    DETECTED="$(ifconfig 2>/dev/null | sed -n 's/.*inet \(addr:\)\{0,1\}\([0-9.]*\).*/\2/p' \
      | grep -v '^127\.' | head -1 || true)"
  fi
  COLLECTOR_IP="$DETECTED"
fi

if [ -z "$COLLECTOR_IP" ]; then
  say ""
  say "Could not work out this machine's IP automatically."
  say "Find it with ONE of these, then re-run with it:"
  say "    ip route get $PROBE_IP          # look for 'src'"
  say "    hostname -I"
  say ""
  say "    COLLECTOR_IP=10.x.x.x $0"
  die "need COLLECTOR_IP"
fi
say "  $COLLECTOR_IP"
say "  this is the ONLY address the hypervisors will accept WinRM from."
say "  It must be the box that RUNS the collector. If that is not this machine,"
say "  re-run as: COLLECTOR_IP=<that box's ip> $0"

# --- 2. CA and client certificate -------------------------------------------
mkdir -p "$CERTS"
# Everything below uses file names relative to the certs directory, so that the
# native openssl and the containerised one (which mounts this directory as its
# working directory) behave identically. Without this cd the native path writes
# the CA into the repo root and then cannot find its own config file.
cd "$CERTS"
if [ -f "$CERTS/gatus-winrm-ca.crt" ] && [ "$FORCE" -eq 0 ]; then
  step "CA already exists, reusing it"
else
  step "Creating the CA and the collector's client certificate"
  rm -f "$CERTS"/gatus-winrm-ca.* "$CERTS"/gatus-client.* "$CERTS"/CA_THUMBPRINT
  cat > "$CERTS/client.cnf" <<CONF
[ req ]
distinguished_name = dn
prompt             = no
[ dn ]
CN = Gatus WinRM collector
[ ext ]
basicConstraints = critical,CA:FALSE
keyUsage         = critical,digitalSignature,keyEncipherment
extendedKeyUsage = critical,clientAuth
subjectAltName   = otherName:msUPN;UTF8:${UPN}
CONF
  # The DN goes in a config file rather than -subj. A /CN=... argument gets
  # rewritten into a filesystem path by MSYS on Windows (Git Bash), which fails
  # with "subject name is expected to be in the format /type0=value0". A config
  # file behaves the same everywhere.
  cat > "$CERTS/ca.cnf" <<CONF
[ req ]
distinguished_name = dn
prompt             = no
x509_extensions    = ext
[ dn ]
CN = Gatus WinRM CA
[ ext ]
basicConstraints = critical,CA:TRUE,pathlen:0
keyUsage         = critical,keyCertSign,cRLSign
subjectKeyIdentifier = hash
CONF
  ssl req -x509 -newkey rsa:4096 -sha256 -days "$DAYS" -nodes \
    -keyout gatus-winrm-ca.key -out gatus-winrm-ca.crt -config ca.cnf 2>/dev/null
  ssl req -newkey rsa:3072 -sha256 -nodes -keyout gatus-client.key \
    -out gatus-client.csr -config client.cnf 2>/dev/null
  ssl x509 -req -in gatus-client.csr -sha256 -days "$DAYS" \
    -CA gatus-winrm-ca.crt -CAkey gatus-winrm-ca.key -CAcreateserial \
    -extfile client.cnf -extensions ext -out gatus-client.crt 2>/dev/null
  rm -f "$CERTS/gatus-client.csr" "$CERTS/gatus-winrm-ca.srl"
  say "  done"
fi

THUMB="$(ssl x509 -in gatus-winrm-ca.crt -noout -fingerprint -sha1 \
  | sed 's/.*=//; s/://g' | tr -d '\r\n' | tr 'a-f' 'A-F')"
printf '%s\n' "$THUMB" > "$CERTS/CA_THUMBPRINT"
say "  CA thumbprint: $THUMB"

# --- 3. a server certificate per host ---------------------------------------
step "Creating a server certificate for each hypervisor"
printf '%s' "$HOSTS" | while IFS='|' read -r SHORT FQDN IPS; do
  [ -n "${SHORT:-}" ] || continue
  LOW="$(printf '%s' "$SHORT" | tr 'A-Z' 'a-z')"
  if [ -f "$CERTS/${LOW}-winrm.pfx" ] && [ "$FORCE" -eq 0 ]; then
    say "  $SHORT already has one"
    continue
  fi
  SAN="DNS:${FQDN},DNS:${SHORT}"
  OLDIFS="$IFS"; IFS=','
  for ip in $IPS; do SAN="${SAN},IP:${ip}"; done
  IFS="$OLDIFS"
  cat > "$CERTS/h.cnf" <<CONF
[ req ]
distinguished_name = dn
prompt             = no
[ dn ]
CN = ${FQDN}
[ ext ]
basicConstraints = critical,CA:FALSE
keyUsage         = critical,digitalSignature,keyEncipherment
extendedKeyUsage = critical,serverAuth
subjectAltName   = ${SAN}
CONF
  ssl req -newkey rsa:3072 -sha256 -nodes -keyout "h-${LOW}.key" \
    -out "h-${LOW}.csr" -config h.cnf 2>/dev/null
  ssl x509 -req -in "h-${LOW}.csr" -sha256 -days "$DAYS" \
    -CA gatus-winrm-ca.crt -CAkey gatus-winrm-ca.key -CAcreateserial \
    -extfile h.cnf -extensions ext -out "h-${LOW}.crt" 2>/dev/null
  PW="$(ssl rand -base64 24 | tr -d '\r\n')"
  ssl pkcs12 -export -inkey "h-${LOW}.key" -in "h-${LOW}.crt" \
    -certfile gatus-winrm-ca.crt -out "${LOW}-winrm.pfx" -passout "pass:${PW}" 2>/dev/null
  printf '%s\n' "$PW" > "$CERTS/${LOW}-winrm.pass"
  rm -f "$CERTS/h-${LOW}.csr" "$CERTS/h-${LOW}.key" "$CERTS/h-${LOW}.crt" \
        "$CERTS/gatus-winrm-ca.srl" "$CERTS/h.cnf"
  say "  $SHORT  ($SAN)"
done

chmod 600 "$CERTS"/*.key "$CERTS"/*.pass "$CERTS"/*.pfx 2>/dev/null || true

# --- 4. the single self-contained PowerShell script -------------------------
step "Writing $OUT"
b64() { ssl base64 -A -in "$1"; }

{
  cat <<PSHEAD
# Gatus WinRM setup. Generated $(date -u '+%Y-%m-%d %H:%M UTC') by setup-winrm.sh.
#
# Copy this one file to a hypervisor and run it ELEVATED. No arguments.
# It finds its own certificate by this machine's hostname.
#
#   powershell -ExecutionPolicy Bypass -File .\\gatus-winrm-setup.ps1
#
# It locks WinRM down to: HTTPS only, from ${COLLECTOR_IP} only, client
# certificate only, and a JEA endpoint that can run exactly one read-only
# command. Nothing else can get in and nothing else can be run.
#
# Everything needed is embedded below. Delete this file from the host when done.
\$ErrorActionPreference = 'Stop'

\$CollectorIp  = '${COLLECTOR_IP}'
\$CaThumbprint = '${THUMB}'
\$AccountName  = '${ACCOUNT}'
\$ConfigName   = 'GatusInventory'
\$CaB64        = '$(b64 "$CERTS/gatus-winrm-ca.crt")'

# hostname -> embedded server certificate
\$Pfx = @{
PSHEAD

  printf '%s' "$HOSTS" | while IFS='|' read -r SHORT FQDN IPS; do
    [ -n "${SHORT:-}" ] || continue
    LOW="$(printf '%s' "$SHORT" | tr 'A-Z' 'a-z')"
    printf "  '%s' = @{ Data = '%s'; Pass = '%s' }\n" \
      "$(printf '%s' "$SHORT" | tr 'a-z' 'A-Z')" \
      "$(b64 "$CERTS/${LOW}-winrm.pfx")" \
      "$(tr -d '\r\n' < "$CERTS/${LOW}-winrm.pass")"
  done

  echo '}'
  echo ''
  cat "$HERE/winrm-harden-body.ps1"
} > "$OUT"

say "  $(wc -c < "$OUT" | tr -d ' ') bytes, covers $(printf '%s' "$HOSTS" | grep -c '|') hosts"

cat <<DONE

===============================================================
DONE on this side. Two things left.

1. Copy this ONE file to a hypervisor:
     $OUT

2. On the hypervisor, in an ADMIN PowerShell, run:
     powershell -ExecutionPolicy Bypass -File .\\gatus-winrm-setup.ps1

   No arguments. Repeat on each host.

   Do MS-HV01 first. Do ONA-HV1 and ONA-HV2 LAST, they are the only
   two reporting right now.

Then on this box, deploy (this RELEASE adds new containers, so a plain
restart fails with "No such container"):
     ./update.sh
     docker logs -f hv-collector

If the SMB rows need credentials, the new keys are in .env.example:
     ./env-merge.sh .env.example .env    # keeps your values, adds new keys
     nano .env                           # fill in SMB_USER and SMB_PASS

Also: move $CERTS/gatus-winrm-ca.key somewhere offline when
you are finished. It can mint a certificate for every host.
===============================================================
DONE
