# WinRM setup for the hypervisor collector

## Do this

**1. On the box that runs Gatus** (the one with `docker compose`):

```bash
cd /path/to/Gatus
./collector/setup-winrm.sh
```

It works out this machine's IP, makes all the certificates, and writes one file:
`collector/gatus-winrm-setup.ps1`.

**2. Copy that one file to a hypervisor. In an admin PowerShell:**

```powershell
powershell -ExecutionPolicy Bypass -File .\gatus-winrm-setup.ps1
```

No arguments. It picks its own certificate by hostname. Delete it from the host
afterwards. Repeat per host.

Do **MS-HV01 first**. Do **ONA-HV1 and ONA-HV2 last**: they are the only two
reporting today, and this removes the old plaintext path they currently use.

**3. Back on the Gatus box:**

```bash
docker compose restart hv-collector && docker logs -f hv-collector
```

Done.

## If step 1 cannot find the IP

Only happens on a box without `ip` or `hostname -I`:

```bash
COLLECTOR_IP=10.x.x.x ./collector/setup-winrm.sh
```

It must be the IP of the box that **runs the collector**. That address is the
only one the hypervisors will accept WinRM from, so a wrong value locks the
collector out and every host reads `host did not answer on any address`.

## Requirements

- Gatus box: `openssl` (or Docker, used automatically if openssl is missing).
- Hypervisors: nothing. The script is plain PowerShell 5.1 and carries
  everything it needs inside it.

## What it locks down

| Layer | Effect |
| --- | --- |
| Network | HTTPS on 5986 only, firewall allows one source address, plaintext 5985 listener removed, Basic auth and unencrypted traffic off |
| Key | Client certificate instead of a password, mapped by issuing CA plus UPN, and scoped to the JEA endpoint so it cannot open a shell or run winrs |
| Capability | A JEA endpoint whose only callable command is `Get-GatusInventory`, in NoLanguage mode, privileged work under a virtual account |
| Account | `svc-gatus-ro`, member of no group, denied interactive, RDP, batch and service logon (verified, the script fails if it cannot apply that) |

Steal the certificate and be on the permitted address, and you can read
inventory. Nothing else. You cannot stop a VM.

`Get-VM` normally needs Hyper-V Administrators, which can also delete guests.
JEA is what avoids handing that out.

Transcripts of every session land in `C:\ProgramData\GatusInventory\Transcripts`
as the audit trail.

## Two files to keep safe

Both are gitignored. Both can authenticate to every hardened host:

- `collector/winrm-certs/gatus-winrm-ca.key` — move it offline when finished.
  Not needed at runtime, only to issue a replacement client certificate.
- `collector/gatus-winrm-setup.ps1` — embeds every host's private key. Delete it
  once the rollout is done; regenerate any time with `setup-winrm.sh`.

The collector container only ever sees three files: the client certificate, its
key, and the CA certificate. Not the CA key.

## Rollout is safe to do gradually

`HV_WINRM_MODE=auto` in `docker-compose.yml` tries the hardened path and falls
back to the old password path, so hardened and un-hardened hosts both keep
reporting. Each row says which transport it used.

When all eleven are done, set `HV_WINRM_MODE=jea` and delete `HV_USER` and
`HV_PASS` from `.env`.

## Undo one host

```powershell
Unregister-PSSessionConfiguration -Name GatusInventory -Force
Get-ChildItem WSMan:\localhost\ClientCertificate | ForEach-Object {
    Remove-Item "WSMan:\localhost\ClientCertificate\$($_.Name)" -Recurse -Force }
Remove-NetFirewallRule -DisplayName 'Gatus WinRM (HTTPS, collector only)'
Remove-LocalUser svc-gatus-ro
Restart-Service WinRM
```

## Adding a hypervisor later

Add it to `HOSTS` in `collector/setup-winrm.sh` and to `HOSTS` in
`collector/hv_collector.py`, re-run `setup-winrm.sh`, and run the regenerated
script on the new host only. Existing hosts are untouched.
