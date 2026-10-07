import urllib.request
import json

import sys

base_url = sys.argv[1] if len(sys.argv) > 1 else "http://3.7.29.3:8080"
try:
    req_curr = urllib.request.Request(f"{base_url}/api/config/all")
    with urllib.request.urlopen(req_curr, timeout=5) as resp:
        curr = json.loads(resp.read().decode())
except Exception:
    base_url = "http://localhost:8080"
    req_curr = urllib.request.Request(f"{base_url}/api/config/all")
    with urllib.request.urlopen(req_curr, timeout=5) as resp:
        curr = json.loads(resp.read().decode())

payload = {
    "options_configs": curr.get("options_configs", []),
    "system_configs": {
        "EQUITY_STRATEGY": {
            "entry_limit_offset_ticks": "-2"
        }
    }
}

save_req = urllib.request.Request(
    f"{base_url}/api/config/save",
    data=json.dumps(payload).encode(),
    headers={"Content-Type": "application/json"}
)

with urllib.request.urlopen(save_req) as resp:
    res = json.loads(resp.read().decode())
    print("SUCCESS:", res)

# Verify runtime audit
audit_req = urllib.request.Request(f"{base_url}/api/config/runtime-audit")
with urllib.request.urlopen(audit_req) as resp:
    audit = json.loads(resp.read().decode())
    print("Audit status:", audit.get("status"))
