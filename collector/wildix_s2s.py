"""Wildix Server-to-Server (S2S) auth, and the one API that spans a WMS Network.

Why this exists
---------------
SIP registrations and user presence are LOCAL to the PBX a user is homed on.
Measured, not assumed: extension 5037 is registered on longlewishv, and asking
longlewishv returns `device_show=registered` with one registration, while asking
longlewiscorporate - the WMS Network MASTER - returns an empty presence and zero
registrations for that same extension. A Simple Token is also bound to one PBX
(it is created against a local `pbxUser`), so a site whose PBX we hold no token
for simply cannot be read through the PBX API.

wda.wildix.com is the exception. Its presence query is keyed on COMPANY, not on
PBX, so one call covers every PBX in the network including ones we have no local
credential for. It does not accept Simple Tokens (401); it wants S2S, a Company
API Key, or CPA. Hence this module.

Credentials come from an S2S application: WMS -> PBX -> Integrations ->
Applications -> S2S, which yields an App ID and a Secret key. The secret is never
transmitted - it only signs the JWT.

Note for later: Wildix has announced that S2S and Simple Token are both
deprecated on 1 May 2027 in favour of Company API Keys. Company API Keys need
WMS 7.09 for PBX routes and this estate is on 7.08, and in any case they do not
cover the registrations or presence routes, so this path is the migration target
rather than a detour.
"""
import base64
import hashlib
import hmac
import json
import os
import ssl
import time
import urllib.error
import urllib.parse
import urllib.request

WDA_HOST = "wda.wildix.com"
HTTP_TIMEOUT = 20
EXPIRE_SECONDS = 300


def _b64(raw):
    """base64url without padding, which is what JWT uses."""
    return base64.urlsafe_b64encode(raw).rstrip(b"=").decode()


def canonical_group(pairs):
    """One group of the canonical request string.

    Lowercase every key, trim every value, sort by key, then concatenate
    "key:value;". Nested values recurse as "idx:value;" so a list renders
    "param:0:a;1:b;;". Booleans collapse to 0/1. An empty group contributes the
    empty string.
    """
    def render(value):
        if isinstance(value, bool):
            return "1" if value else "0"
        if isinstance(value, dict):
            return "".join(f"{k}:{render(v)};" for k, v in sorted(value.items()))
        if isinstance(value, (list, tuple)):
            return "".join(f"{i}:{render(v)};" for i, v in enumerate(value))
        return str(value).strip()

    return "".join(f"{str(k).lower()}:{render(v)};"
                   for k, v in sorted(pairs.items(), key=lambda kv: str(kv[0]).lower()))


def build_jwt(app_id, secret, app_name, method, uri, host, get_params=None, post_params=None):
    """An S2S JWT for one specific request.

    The signature covers the request itself, not just the clock, so a token is
    not reusable across calls: the canonical string is
    method + uri + headers + get params + post params, hashed with sha256, and
    that hash travels inside the JWT payload.
    """
    headers = {"Host": host, "X-APP-ID": app_id}
    canonical = (method.upper() + uri
                 + canonical_group(headers)
                 + canonical_group(get_params or {})
                 + canonical_group(post_params or {}))
    digest = hashlib.sha256(canonical.encode()).hexdigest()

    now = int(time.time())
    header = {"typ": "JWT", "alg": "HS256"}
    payload = {
        "iss": app_name,
        "iat": now,
        "exp": now + EXPIRE_SECONDS,
        # The header NAMES here must match exactly the headers folded into the
        # canonical string above, or the PBX recomputes a different hash.
        "sign": {"alg": "sha256", "headers": list(headers), "hash": digest},
    }
    signing_input = (_b64(json.dumps(header, separators=(",", ":")).encode()) + "."
                     + _b64(json.dumps(payload, separators=(",", ":")).encode()))
    signature = hmac.new(secret.encode(), signing_input.encode(), hashlib.sha256).digest()
    return signing_input + "." + _b64(signature), canonical


def credentials():
    """(app_id, secret, app_name) from the environment, or None if unset."""
    app_id = (os.environ.get("WILDIX_S2S_APP_ID") or "").strip()
    secret = (os.environ.get("WILDIX_S2S_SECRET") or "").strip()
    name = (os.environ.get("WILDIX_S2S_APP_NAME") or "gatus").strip()
    if not app_id or not secret:
        return None
    return app_id, secret, name


def _ctx():
    if os.environ.get("VERIFY_TLS", "1") == "0":
        c = ssl.create_default_context()
        c.check_hostname = False
        c.verify_mode = ssl.CERT_NONE
        return c
    return None


def s2s_request(host, method, uri, body=None, get_params=None):
    """Issue one S2S-signed request. Returns (status, parsed_json)."""
    creds = credentials()
    if creds is None:
        raise ValueError("WILDIX_S2S_APP_ID / WILDIX_S2S_SECRET are not set")
    app_id, secret, app_name = creds
    token, _ = build_jwt(app_id, secret, app_name, method, uri, host,
                         get_params=get_params, post_params=body)
    url = f"https://{host}{uri}"
    if get_params:
        url += "?" + urllib.parse.urlencode(get_params)
    headers = {
        "Host": host,
        "X-APP-ID": app_id,
        "Authorization": f"Bearer {token}",
        "Accept": "application/json",
    }
    data = None
    if body is not None:
        headers["Content-Type"] = "application/json"
        data = json.dumps(body).encode()
    req = urllib.request.Request(url, headers=headers, method=method, data=data)
    with urllib.request.urlopen(req, timeout=HTTP_TIMEOUT, context=_ctx()) as resp:
        return resp.getcode(), json.loads(resp.read() or b"{}")


# Telephony states that mean "this handset is talking to the PBX right now".
# RT is a Wildix relay state and still means the device is attached.
REGISTERED_STATES = {"REGISTERED", "RINGING", "TALKING", "RT"}


def query_presence(extensions, company=None):
    """Presence for up to 500 extensions, across every PBX in the company.

    Returns {extension: {"registered": bool, "telephony": str, "status": str}}.
    The filter is company-scoped rather than PBX-scoped, which is the entire
    point: it reaches extensions homed on a PBX we cannot authenticate against.
    """
    out = {}
    exts = [str(e) for e in extensions if str(e).strip()]
    for start in range(0, len(exts), 500):
        chunk = exts[start:start + 500]
        filt = [({"company": company, "extension": e} if company else {"extension": e})
                for e in chunk]
        _, data = s2s_request(WDA_HOST, "POST", "/v2/stream/presence/query_multiple",
                              body={"filter": filt})
        for p in (data.get("presences") or []):
            ext = str(p.get("extension") or "")
            if not ext:
                continue
            telephony = str(p.get("telephony") or "").upper()
            out[ext] = {
                "registered": telephony in REGISTERED_STATES,
                "telephony": telephony,
                "status": str(p.get("status") or ""),
            }
    return out
