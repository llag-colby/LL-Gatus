# SITE_MAP expansion — verification report

**Target:** `C:\Users\colby.west\Desktop\Projects\LL-Telemetry\telemetry\api\main.py` (`SITE_MAP`, lines 48-75)
**Source of truth:** `C:\Users\colby.west\Desktop\Projects\Gatus\config.yaml` (`icmp://` endpoints)
**Status of change:** uncommitted working-tree modification to `telemetry/api/main.py`.

## Verdict summary

| # | Check | Verdict |
|---|---|---|
| 1 | Independent extraction of icmp endpoints | **PASS** |
| 2 | Diff vs SITE_MAP (missing / extra / wrong site / typo'd octet) | **PASS** — 20/20 IPs exact, zero transcription errors |
| 3 | Duplicate IPs mapped to different names | **PASS** — Decatur GMC/KIA is the only collision |
| 4 | `resolve_site()` correctness with the larger list | **PASS (safe) / FAIL (stated rationale)** |
| 5 | Site names match Gatus vocabulary | **FAIL** — `"Decatur"` is not a Gatus name |
| 6 | `site` column width | **PASS** — 19 chars max vs VARCHAR(48) |
| 7 | Premise: will WAN /32 matching ever fire | **FAIL** — dead weight on the real topology |

The IP data itself is clean. Everything that fails is about naming and about whether the layer can ever execute.

---

## 1. Independent extraction — PASS

Extracted by walking `config.yaml` and binding each `icmp://` URL to the most recent preceding `- name:`, without reference to `SITE_MAP`.

**21 icmp endpoints, 20 unique IPs.**

| IP | Gatus `name:` | config.yaml line |
|---|---|---|
| 50.225.219.73 | Muscle Shoals | 34 |
| 45.17.37.190 | Muscle Shoals | 39 |
| 99.28.23.6 | Ivory Tower | 46 |
| 162.17.79.210 | Ivory Tower | 51 |
| 12.247.225.57 | Prattville | 58 |
| 24.42.189.129 | Prattville | 63 |
| 75.114.33.97 | Bessemer | 70 |
| 71.45.253.161 | Bessemer | 75 |
| 23.119.67.94 | Florence | 82 |
| 70.89.72.118 | Florence | 87 |
| 162.199.109.198 | Hoover | 94 |
| 24.197.59.161 | Hoover | 99 |
| 45.25.124.78 | Tuscumbia | 106 |
| 96.82.22.254 | Tuscumbia | 111 |
| 12.247.225.93 | Alabaster | 118 |
| 47.48.167.2 | Alabaster | 123 |
| 12.77.92.1 | Cullman | 130 |
| 71.8.47.121 | Cullman | 135 |
| 71.86.70.190 | Decatur GMC | 142 |
| 107.222.141.62 | Alabaster Body Shop | 151 |
| 71.86.70.190 | Decatur KIA | 160 |

No other `icmp://` endpoints exist in the file. The `name:` values at lines 174-241 belong to non-icmp (phone / UniFi) endpoints and contribute no addresses.

**Note on the stated premise:** the change was described as adding *21* WAN /32 entries. It actually adds **20**. That is correct behavior, not an omission — 71.86.70.190 appears twice in `config.yaml` (GMC and KIA) and was properly deduplicated to a single map entry. The count in the change description is off by one; the code is right.

## 2. Diff against SITE_MAP — PASS

Compared as sorted sets, byte-for-byte, not by eye.

- **IPs in config.yaml missing from SITE_MAP: NONE.** All 20 unique addresses present.
- **IPs in SITE_MAP not in config.yaml: NONE.** No invented or stale entries.
- **Typo'd octets: NONE.** The two IP sets are byte-identical. Every octet is numeric, in range 0-255, and each address has exactly four octets.
- **Wrong site name: 1 of 20**, and it is the deliberate Decatur merge (below). The other 19 rows match Gatus character-for-character including spacing and capitalization.

The only differing row:

```
config.yaml   71.86.70.190  ->  "Decatur GMC"  AND  "Decatur KIA"
SITE_MAP      71.86.70.190  ->  "Decatur"
```

One observation worth a human check, not a code defect: **12.247.225.57 (Prattville) and 12.247.225.93 (Alabaster)** sit in the same AT&T /24 but are attributed to sites ~50 miles apart. `SITE_MAP` faithfully reproduces what `config.yaml` says, so this is not a transcription error — but if one of those two Gatus monitors was itself mis-entered, the error is now duplicated into a second system. Worth confirming against carrier records once.

## 3. Duplicate IPs mapped to different names — PASS

- **Within `SITE_MAP`:** zero duplicate /32 keys. Every CIDR appears exactly once, so no entry is unreachable behind an earlier identical one.
- **Within `config.yaml`:** exactly one IP is bound to more than one `name:` — `71.86.70.190` (Decatur GMC + Decatur KIA). Confirmed as the **only** such collision across all 21 endpoints.
- Nothing else collides. Sites legitimately sharing a *name* across two different IPs (the WAN1/WAN2 pairs) are expected and correct.

The collapse to `"Decatur"` is the only defensible resolution given one address, and the comment at lines 46-47 documents it accurately. The correctness problem it creates is a naming problem, covered in check 5.

## 4. `resolve_site()` with the larger list — PASS on safety, FAIL on the stated rationale

```python
for cidr, name in SITE_MAP:
    if addr in ipaddress.ip_network(cidr):
        return name
```

**Does putting LAN subnets first give them priority? No — and it does not need to.**

I checked every one of the 24 entries against every other for overlap: **there are zero overlapping pairs anywhere in the list.** No address can match two entries, so first-match ordering never actually arbitrates anything. The list could be shuffled at random with identical results.

The LAN-over-WAN priority that the header comment describes ("Two layers, checked in order") is **not** delivered by list order at all. It is delivered by the caller at line 273:

```python
site = resolve_site(run.ip) or resolve_site(src)
```

Two separate calls with two different inputs. The comment attributes the behavior to the wrong mechanism. Harmless today, but if someone later adds a broad WAN aggregate (say a `/24` instead of a `/32`) believing "LAN first" protects them, ordering still will not save them — the two layers are only separated by which argument they get.

**Could a /32 WAN entry ever shadow a LAN subnet, or vice versa?** No. All four LAN entries are inside 10.0.0.0/8; all 20 WAN entries are public. The address spaces are disjoint.

**Are any WAN /32s inside RFC1918 space?** No. Scanned all 20 for 10/8, 172.16/12, 192.168/16, 127/8, 169.254/16 and multicast/reserved — **all 20 are public**, as intended.

**Do any LAN subnets overlap each other?** No.

| CIDR | Range | Site |
|---|---|---|
| 10.15.100.0/22 | 10.15.100.0 – 10.15.103.255 | Ivory Tower |
| 10.15.104.0/22 | 10.15.104.0 – 10.15.107.255 | Ivory Tower |
| 10.6.81.0/24 | 10.6.81.0 – 10.6.81.255 | Decatur→ n/a, Muscle Shoals |
| 10.10.1.0/24 | 10.10.1.0 – 10.10.1.255 | Decatur |

Both /22s sit on legal /22 boundaries (100 and 104 are multiples of 4) and are adjacent but non-overlapping. They cannot be merged into a single /21 (100 is not a multiple of 8), so both entries are genuinely required.

**Residual risks found in the function, none introduced by this change:**

1. **LAN short-circuit is unrecoverable.** `resolve_site(run.ip) or resolve_site(src)` means a LAN match wins outright. `10.10.1.0/24` is an extremely common default range. If any other dealership reuses it, every machine there silently reports as `Decatur` and the WAN fallback is never consulted to correct it — exactly the silent mis-attribution this review is meant to catch. The four LAN subnets are only trustworthy if the org guarantees a globally unique private addressing scheme.
2. **IPv6 is silently unmappable.** An IPv6 `run.ip` or `X-Forwarded-For` parses fine, then `IPv6Address in IPv4Network` returns `False` for all 24 entries, yielding `None`. Safe (no wrong answer), but it means an IPv6-egressing site would never match its /32 even if the layer otherwise worked.
3. **Minor inefficiency.** `ipaddress.ip_network(cidr)` is re-constructed inside the loop on every call — up to 24 objects per call, 48 per ingest. Precomputing the networks once at module load would remove it. Not a correctness issue.

## 5. Site names vs Gatus vocabulary — FAIL

19 of 20 names match `config.yaml` exactly, character for character. The exception breaks correlation:

- **`"Decatur"` does not exist anywhere in `config.yaml`.**
- **`"Decatur GMC"` and `"Decatur KIA"` have no counterpart in `SITE_MAP`.**

All other names verified exact: `Alabaster`, `Alabaster Body Shop`, `Bessemer`, `Cullman`, `Florence`, `Hoover`, `Ivory Tower`, `Muscle Shoals`, `Prattville`, `Tuscumbia`. No case differences, no trailing whitespace, no `&`/`and` variance.

The header comment at lines 43-44 claims "Site names match Gatus's vocabulary exactly so the two systems can be correlated." **That claim is false for Decatur.** Any join keyed on site name will produce three orphans: telemetry `Decatur` matches nothing on the Gatus side, and Gatus `Decatur GMC` / `Decatur KIA` match nothing on the telemetry side. A dealership that is 2 of 12 Gatus cards becomes a silent gap in any combined view.

**Second, larger naming problem — the LAN entry was also renamed.** Committed `HEAD` had:

```python
("10.10.1.0/24",   "Bramlett / Decatur"),
```

The change makes it `"Decatur"`. That is a **data-continuity break in the database, not just a label change**:

- `runs.site` is a denormalized string written at ingest. Existing rows already say `Bramlett / Decatur`; new rows will say `Decatur`.
- `/api/v1/stats` does `GROUP BY site` (main.py:405-408). The site will **split into two tiles** on the dashboard, each with partial counts, for the full 30-day retention window.
- `/api/v1/runs` filters with `site = %s` (main.py:334-337) and the dashboard's site filter passes the clicked tile's exact string — so clicking either tile returns only half the history.
- No backfill `UPDATE runs SET site='Decatur' WHERE site='Bramlett / Decatur'` accompanies the change.

The dashboard itself (`telemetry/dashboard/index.html`) builds its site list entirely from `st.by_site` with no hardcoded names, so nothing breaks structurally — it will just render two tiles where there should be one.

**Documentation is now stale on the same point:**
- `LL-Telemetry\telemetry\README.md:184` still reads `| 10.10.1.0/24 | Bramlett / Decatur |`, and the §9 table still lists only the original four subnets with no mention of the WAN layer.
- `LL-Telemetry\.tk\notes\README.md:175` and `.tk\notes\main.py:27` carry the same stale copy.

**Test coverage gap:** `telemetry\api\tests\test_main.py:98-104` still exercises only `Ivory Tower`, `Muscle Shoals`, `8.8.8.8 -> None`, `None`, and `"not-an-ip"`. Those all still pass (8.8.8.8 is not in the new map). But there is **no assertion for any of the 20 new WAN entries and none for the Decatur rename**, so a future typo in this block would ship silently. Given that mis-attribution is the stated risk, this block is the one that most deserves a test.

## 6. `site` column width — PASS

| Source | Value |
|---|---|
| `LL-Telemetry\telemetry\db\01-schema.sql:28` | `site VARCHAR(48) NULL` |
| `main.py:89` `COLUMN_WIDTHS["site"]` | `48` |

The two agree exactly, and `clip()` (main.py:105-110) truncates rather than rejecting, so an oversized value could never 4xx a run.

Longest site name across **both** systems is `Alabaster Body Shop` at **19 characters** — 29 characters of headroom. Every name: Hoover 6, Cullman 7, Decatur 7, Bessemer 8, Florence 8, Alabaster 9, Tuscumbia 9, Prattville 10, Decatur GMC 11, Decatur KIA 11, Ivory Tower 11, Muscle Shoals 13, Alabaster Body Shop 19. **Zero clipping risk.**

(For reference, the old `Bramlett / Decatur` was 18 — also fine. Width was never the constraint.)

## 7. Premise sanity check — FAIL. This layer is dead weight.

Stated plainly: **on the real topology, the WAN /32 layer will never fire.** It is not a fallback; it is 20 lines that can only be reached by an attacker.

The ingest path, verified against config rather than assumed:

1. **The source address is `client_ip(request)`** (main.py:199-204) — the left-most entry of `X-Forwarded-For`, falling back to `request.client.host`.
2. **Caddy is the only thing in front of the API.** `telemetry\docker-compose.yml` publishes ports on `caddy` only; the API has no port mapping and is reachable solely as `api:8080` on the compose network. Caddy uses a bare `reverse_proxy api:8080`, so XFF is populated with the peer address Caddy sees.
3. **That peer address is always private.** `telemetry\caddy\Caddyfile:8` binds `telemetry.longlewis.local, 10.15.102.8` with an internal-CA cert — no ACME, no public name. `infra\IT-Telemetry-01-FirstBoot.md:96-98` sets `ufw allow from 10.0.0.0/8` only. `telemetry\README.md:21` states "Internal only. No public DNS, no NAT, no firewall publish… Reachable from every site over the existing VPN tunnels." `docs\SECURITY.md` says the same.
4. **Clients post to an internal name.** `agent\LL-Report-Block.ps1:22` — `https://telemetry.longlewis.local/api/v1/runs`, an AD-zone record resolving to `10.15.102.8`. Identical in all three `field-scripts\LL-*.cmd` and the NinjaOne flush snippet.
5. **`run.ip` is the machine's own LAN IP.** `LL-Report-Block.ps1:44-52` takes the IPv4 address on the lowest-metric default-route interface — always a 10.x address, never a public one.

So: traffic **never traverses the internet** to reach the collector. It rides site-to-site VPN tunnels to a private VM. The source IP Caddy observes is a 10.x LAN or tunnel address. A site's public egress IP is therefore **never** present in either `run.ip` or `src`, and no 20-entry list of public /32s can ever match.

The one path that *can* reach it makes things worse, not better. `Caddyfile:1-6` sets `trusted_proxies static private_ranges`, so every RFC1918 client — i.e. every field PC — is treated as a trusted proxy and its self-supplied `X-Forwarded-For` is honored. `client_ip()` then takes the left-most entry. **The only way a WAN /32 ever matches is a client spoofing its own `X-Forwarded-For` header.** The layer's sole reachable trigger is forgery, and the same weakness already lets any endpoint forge the `source_ip` column and evade the per-IP rate limiter.

The practical consequence: the sites this change was meant to rescue — Prattville, Bessemer, Florence, Hoover, Tuscumbia, Alabaster, Alabaster Body Shop, Cullman — **will still land as `unmapped`**. Nothing observable improves. The real fix is confirming those sites' LAN subnets and adding them to layer 1, which is exactly what the pre-existing comment at lines 37-38 already prescribes and what the `unmapped_subnets` query at main.py:431-441 exists to surface.

---

## What is and is not wrong

**The transcription is flawless.** 20/20 IPs, every octet correct, no extras, no omissions, no duplicates, no RFC1918 leakage, no overlaps, no width risk. If the goal was "did the IPs get copied correctly," the answer is an unqualified yes.

**Three things need a decision before this ships:**

1. **The `Bramlett / Decatur` → `Decatur` rename splits existing DB rows into two dashboard tiles** with no backfill. Highest-impact concrete defect; it degrades a working view.
2. **`"Decatur"` is not a Gatus name,** so the comment's correlation guarantee is false and any name-keyed join orphans three values.
3. **The 20 WAN /32s cannot execute on this topology.** They are inert except via header spoofing, and they carry a real cost: they read as coverage. A future maintainer looking at `by_site` and seeing sites still unmapped may conclude the WAN IPs are wrong and go re-verify them, when the actual gap is missing LAN subnets.

**Lower priority:** stale README table at `telemetry\README.md:184` and §9; no test coverage for any of the 20 new entries; `ip_network()` rebuilt per iteration.

*Verified only; nothing was modified.*
