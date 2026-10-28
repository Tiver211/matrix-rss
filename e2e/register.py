import json, urllib.request, urllib.error
BASE = "http://localhost:6167/_matrix/client/v3"
def post(path, body):
    req = urllib.request.Request(BASE + path, json.dumps(body).encode(), {"content-type": "application/json"})
    try:
        with urllib.request.urlopen(req) as r: return r.status, json.load(r)
    except urllib.error.HTTPError as e: return e.code, json.load(e)
for name in ["rssbot", "alice", "bob", "carol"]:
    _, first = post("/register", {})
    status, body = post("/register", {"username": name, "password": name + "-pass",
        "auth": {"type": "m.login.registration_token", "token": "testtoken", "session": first["session"]}})
    print(name, status, body.get("user_id") or body.get("error"))
