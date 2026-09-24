# Writing a Nilda plugin in a language that is not Go

Nilda talks to plugins over **gRPC**, and gRPC is not a Go idea. If your language can serve a gRPC
service and write a line to standard output, it can be a Nilda plugin — with the same capabilities and the
same isolation as one written in Go.

**One thing differs today, and it is said here rather than discovered after you have built it: marketplace
distribution is Go-only.** Nilda's publish step and the marketplace identify an artifact from the build
information a Go compiler writes into it, which a Python or Rust executable does not carry, so they refuse
it. A plugin in any other language installs by **sideload** — the site owner uploads the package (§9).

This document is the whole contract. It exists because the Go SDK hides all of it, and hiding something is
not the same as requiring it.

> **Should you?** If you write Go, use the SDK (`docs/PLUGIN_SDK.md`) — it does everything below for you
> and you write three methods. Read this if Go is not your language, or if you are writing an SDK for one.

---

## The shape of a plugin

A plugin is a **program Nilda starts**. Not a library it loads, not a service you deploy: Nilda runs your
executable as a child process, talks to it over a local gRPC connection, and kills it when the plugin is
disabled. That is the whole isolation model — your crash is your crash, your memory limit is yours, and
your process holds no handle to Nilda's database.

```
  Nilda (parent)                      your plugin (child process)
        │  spawn + stdout handshake            │
        ├─────────────────────────────────────▶│
        │  gRPC: PluginService                  │
        ├─────────────────────────────────────▶│   ← Nilda calls you
        │  gRPC: HostService (over the broker)  │
        │◀─────────────────────────────────────┤   ← you call Nilda
```

---

## 1. The contract file

Everything on the wire is defined in one file:

```
plugin-sdk/contract/plugin.proto
```

Generate your bindings from it with your language's protobuf toolchain. Two services:

- **`PluginService`** — you IMPLEMENT this. Nilda calls it.
  `Init`, `HandleHook`, `HandleEvent`, `Health`.
- **`HostService`** — you CONSUME this. It is your only door back into Nilda, and every method is
  capability-gated: a method your manifest did not ask for returns `PermissionDenied`, never data.

The proto is versioned. `ProtocolVersion` is **2** today. Nilda refuses a plugin built against an
incompatible version at the handshake, with a message — never silently.

---

## 2. The handshake

Nilda uses [`hashicorp/go-plugin`](https://github.com/hashicorp/go-plugin) as the process protocol, the
same one Terraform and Vault use for their providers. Your program must do exactly two things at startup.

### 2a. Check the magic cookie

Nilda sets an environment variable before starting you:

```
NILDA_PLUGIN=1b6cf7a2e4nilda98d3f5c0a9b8e7d61
```

If it is absent or different, exit with a human-readable error. This is **not** security, and nothing else
in the handshake is either: your process runs as the SAME operating-system user as Nilda. What Nilda gives
you is fault isolation — your crash is yours — not a security boundary. The cookie exists so somebody who
runs your binary directly gets "run me under Nilda" instead of a gRPC dump.

### 2b. Write one line to stdout, then never write to stdout again

Start your gRPC server on a local address, then print exactly one line:

```
CORE-PROTOCOL-VERSION|APP-PROTOCOL-VERSION|NETWORK-TYPE|NETWORK-ADDR|PROTOCOL|SERVER-CERT
```

For Nilda that is:

| field | value |
|---|---|
| `CORE-PROTOCOL-VERSION` | `1` — go-plugin's own wire version, not ours. Any other value and you will not load |
| `APP-PROTOCOL-VERSION` | `2` — Nilda's `ProtocolVersion` |
| `NETWORK-TYPE` | `tcp` or `unix` |
| `NETWORK-ADDR` | where your gRPC server is listening |
| `PROTOCOL` | `grpc` |
| `SERVER-CERT` | your TLS certificate, DER, base64 **RawStd** encoded — no padding, no PEM header (see below) |

Example:

```
1|2|tcp|127.0.0.1:51423|grpc|MIIBxTCCAWugAwIBA...
```

Then **stop writing to stdout**. Nilda reads that stream as the handshake. Everything else you print —
logs, warnings, stack traces — goes to **stderr**, where Nilda picks it up and re-emits it in the site's
own log (see §5).

### 2c. AutoMTLS

Nilda enables `AutoMTLS`, so the control channel is encrypted and mutually authenticated. This is the part
an SDK earns its keep on; every value below is from go-plugin v1.8.0's `server.go`, not from memory.

1. Read Nilda's client certificate from the environment variable **`PLUGIN_CLIENT_CERT`** — PEM, one
   certificate. Its presence is what tells you AutoMTLS is on; if it is empty, serve without TLS.
2. Generate your own self-signed certificate and key at startup, **in memory**. Do not write them to disk:
   they are valid for one run of one process, and a file is a copy somebody can steal.
3. Serve gRPC with TLS configured as:
   - your certificate and key
   - `ClientAuth: RequireAndVerifyClientCert`
   - `ClientCAs` **and** `RootCAs`: a pool containing only Nilda's certificate
   - `MinVersion: TLS 1.2`
   - `ServerName: "localhost"`
4. Put your certificate's **leaf DER bytes**, base64 `RawStdEncoding` (standard alphabet, **no padding**),
   in the handshake line's last field. Not PEM — the handshake is one line and cannot carry newlines.

Both sides then trust exactly one certificate: each other's. Nothing else on the machine can talk to your
plugin, and you will not talk to anything claiming to be Nilda.

---

## 3. What Nilda calls: `PluginService`

### `Init(InitRequest) → InitResponse`

Called once, before anything else. The request carries everything you are allowed to know:

| field | what it is |
|---|---|
| `plugin_key` | your manifest key. **Nilda's, not yours** — it is what namespaces everything you contribute |
| `granted_capabilities` | exactly what the site owner approved. Check with it; never assume |
| `settings_json` | the values the owner typed on your admin pages. **Secrets are here in plaintext** |
| `api_token` + `api_base_url` + `api_scopes` | your scoped credential for Nilda's own REST API |
| `datastore_dsn` | your own Postgres schema, least-privilege. Empty unless `datastore` was granted |
| `kv_namespace` | your Dragonfly namespace (operations go through `HostService`) |
| `route_prefix` | the URL prefix Nilda proxies to you. Register handlers under it — the **full** path is forwarded |
| `previous_version` | the version you last completed an `Init` at; empty on a fresh install |
| `host_broker_id` | the stream id to dial for `HostService` (see §4) |

You answer with:

- `hooks` — the hook names you subscribe to. Nilda filters this against your grants, so asking for one you
  were not granted is harmless, and **not** asking for one you were granted means you never hear from it.
- `events` — the event types you want.
- `route_addr` — the local address of your own HTTP server, if you were granted `route`.
- `abilities`, `schedules` — see the SDK guide.

**Settings arrive here and nowhere else.** Nilda RESTARTS a plugin when its settings are saved, so a
running plugin can never hold a credential the owner has already replaced. Do not build a watch API; there
is nothing to watch.

### `HandleHook(HookRequest) → HookResponse`

`hook` is a name, `payload` is JSON. Return JSON. The shapes per hook are in the SDK guide and in the
proto's comments. Nilda applies a **5-second timeout** by default — a slow answer fails the call, not the
site.

### `HandleEvent(EventRequest) → EventResponse`

Fire-and-forget notification. Nothing waits for your answer.

### `Health(HealthRequest) → HealthResponse`

Answer promptly. Nilda supervises you: repeated failures disable the plugin.

### What Nilda does when you are slow or failing

Worth knowing before you tune anything on your side, because two of these mean Nilda stops calling you and
that is not a bug you should work around.

- **A 5-second ceiling per call**, above.
- **A circuit breaker.** If more than half your calls fail over a two-minute window — with at least five
  calls in it, so a quiet site cannot trip you on one bad answer — Nilda stops calling you for 30 seconds,
  then lets ONE call through. Two clean answers and it is fully back; a failed probe and the 30 seconds
  start again. **You do not need to expose a recovery endpoint or ask anybody to re-enable you.** Just be
  healthy when the probe arrives.
- **A concurrency bound of 16.** Nilda will never have more than 16 calls open to your process at once, and
  it refuses the 17th on its own side rather than queueing it. So you can size your worker pool to 16 and
  stop there — and if you are a language with a single-threaded runtime, know that you may still be asked
  16 things at once.
- **A shared budget when several plugins build one response.** A public page gives ALL plugins 1.5s
  together and an admin screen 3s, so your deadline on those paths may be much tighter than 5 seconds.
  Honour the gRPC deadline on the context you are handed — do not assume you have the full timeout.

---

## 4. What you call: `HostService`

`Init`'s `host_broker_id` is a stream id on go-plugin's **broker** — a multiplexed connection over the same
transport. Dial it, and you have a `HostService` client.

Every method checks your granted capabilities first:

| method | needs |
|---|---|
| `KVGet` / `KVSet` / `KVDel` / `KVIncr` | `kv` |
| `EmitEvent` | `events` — and a name that is yours: `<your key>.<name>`, or a namespace a capability you hold owns (`commerce.*` is the shop's). Anything else, and Nilda's own names to everyone, is `PERMISSION_DENIED` |
| `SendEmail` | `email` — Nilda fixes the sender, so you can address mail but not forge who it is from |
| `RevokeIdentity` | `auth_provider` — end somebody's sessions. **Never mint, may revoke** |

Content reads and writes are **not** here. They go through Nilda's own REST API with the token from `Init`,
which is the same surface every first-party feature uses: create, edit, publish, schedule, delete, media,
import, GraphQL.

**How often you may call in.** The calls above are bounded per plugin, as Nilda's calls to you are: at most
60 emails a minute, 20 events a second (bursts of 100), 500 key-value calls a second (bursts of 1,000), and
100 lines of your log a second (bursts of 500 — past that a line is counted rather than recorded, and the
count is logged). Past a bound the call answers `RESOURCE_EXHAUSTED` at once rather than queueing; slow down
and retry. A plugin doing its job never meets one — a loop that forgot to stop does.

---

## 5. Logging

Write **hclog-shaped JSON, one object per line, to stderr**:

```json
{"@level":"info","@message":"single sign-on ready","@timestamp":"2026-08-06T00:00:00.000000Z","issuer":"https://acme.okta.com"}
```

| field | notes |
|---|---|
| `@level` | `trace` `debug` `info` `warn` `error` |
| `@message` | the sentence |
| `@timestamp` | `2006-01-02T15:04:05.000000Z07:00` |
| anything else | becomes a structured field |

Nilda parses each line and re-emits it in the site's log at the level you declared, tagged with your plugin
key — which it sets itself, so an attribute of yours cannot forge it. A line that is not JSON is kept
verbatim at debug rather than dropped.

---

## 6. Outbound network

If Nilda's egress policy is on (it is, by default), your outbound HTTP goes through a local proxy that
allows only the hosts your manifest declared. The proxy has to know **who is calling**, and Nilda tells it
for you: the proxy address it puts in `HTTP_PROXY` / `HTTPS_PROXY` carries your plugin key as its user
name (`http://<your key>@127.0.0.1:<port>`). Any HTTP client that takes its proxy from those variables
sends that as `Proxy-Authorization` — on plain requests and on the **CONNECT** that opens an HTTPS tunnel —
so you do nothing beyond using the environment's proxy.

If your client ignores the environment and you configure the proxy by hand, keep the user name, or set the
header `X-Nilda-Plugin: <your plugin key>` on every request AND on the CONNECT. Lose it and every HTTPS call
is refused with "this plugin did not declare that host" while your manifest declares it perfectly — the
Go SDK once shipped exactly that bug.

**One exception you do not declare**: a sign-in plugin's identity provider. Nilda allows the issuer the
site owner typed on your settings page — you cannot name it in advance, because it is different at every
company that installs you.

---

## 7. Your manifest

`plugin.json` — that is the name Nilda's installer reads, whatever language you write in. It goes into the
package beside your executable (§9):

```json
{
  "key": "acme_thing",
  "name": "Acme Thing",
  "version": "1.0.0",
  "sdk_version": "0.10.0",
  "nilda_compat": ">=0.1.0",
  "os": "linux",
  "arch": "amd64",
  "capabilities": ["hooks", "kv"],
  "network": ["api.acme.com"]
}
```

`sdk_version` declares which protocol you speak: every v0 from 0.2 on, and v1, speak protocol 2 —
`ProtocolForSDK` in `handshake.go` is the table. If you are not using an SDK, set it to a version whose
protocol you implement.

**Declare `os` and `arch`.** Nilda reads the platform out of a Go binary's build information; yours has
none, so on a sideload Nilda falls back to what your manifest says. Left out, the install is still permitted
— and a binary built for another platform simply fails to start.

---

## 8. The rules that are not negotiable

These are not go-plugin's; they are Nilda's, and they hold whatever language you write in.

- **You declare; Nilda draws.** No plugin ships JavaScript or CSS into the admin. A widget's settings, an
  admin page, a field type are *descriptions* Nilda renders with its own controls. The one markup a plugin
  returns is a widget's rendered HTML, and Nilda sanitizes it before it reaches a page or the editor's
  canvas — scripts, event handlers, `javascript:` URLs and `style` removed. A script reaches public pages
  only through `render.assets`, as a path under your own route. This is why a bad plugin cannot white-screen
  the admin or steal a session, and it is not going to change.
- **Nilda owns your namespace.** A widget type, a field type, a sign-in method you contribute is
  `<your key>.<your local name>`, assigned by Nilda from the key it is running you under. You cannot claim
  another plugin's name, or a built-in one.
- **You return an assertion, never a decision.** Most visibly for `auth_provider`: you hand over the
  identity provider's signed token and **Nilda** verifies it. You never mint a session, name a user, or set
  a role.
- **Deny by default.** Every capability is approved by the site owner at install. A call you were not
  granted returns `PermissionDenied` — check `granted_capabilities` and fail with a clear message rather
  than discovering it at runtime.

---

## 9. A minimal Python plugin

Sketch, not a library — enough to see there is no Go in it.

```python
import base64, os, sys, socket, datetime
from concurrent import futures
import grpc
import plugin_pb2, plugin_pb2_grpc          # generated from contract/plugin.proto

MAGIC = "1b6cf7a2e4nilda98d3f5c0a9b8e7d61"

def log(level, message, **fields):
    ts = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.%f") + "Z"
    obj = {"@level": level, "@message": message, "@timestamp": ts, **fields}
    print(__import__("json").dumps(obj), file=sys.stderr, flush=True)

class Plugin(plugin_pb2_grpc.PluginServiceServicer):
    def Init(self, req, ctx):
        self.key = req.plugin_key
        self.granted = set(req.granted_capabilities)
        log("info", "ready", plugin=self.key)
        return plugin_pb2.InitResponse(hooks=["content.saved"])

    def HandleHook(self, req, ctx):
        # req.hook is the name, req.payload is JSON bytes. Answer with JSON bytes.
        return plugin_pb2.HookResponse(payload=b'{"ok":true}')

    def HandleEvent(self, req, ctx):
        return plugin_pb2.EventResponse()

    def Health(self, req, ctx):
        return plugin_pb2.HealthResponse(ok=True)

def main():
    if os.environ.get("NILDA_PLUGIN") != MAGIC:
        sys.exit("This is a Nilda plugin. Run it through Nilda, not directly.")

    cert_pem, key_pem, cert_der = self_signed()          # generate in memory
    nilda_cert = os.environ["PLUGIN_CLIENT_CERT"].encode()

    creds = grpc.ssl_server_credentials(
        [(key_pem, cert_pem)], root_certificates=nilda_cert, require_client_auth=True)

    server = grpc.server(futures.ThreadPoolExecutor(max_workers=8))
    plugin_pb2_grpc.add_PluginServiceServicer_to_server(Plugin(), server)
    port = server.add_secure_port("127.0.0.1:0", creds)
    server.start()

    b64 = base64.standard_b64encode(cert_der).decode().rstrip("=")   # RawStd: standard alphabet, no padding
    print(f"1|2|tcp|127.0.0.1:{port}|grpc|{b64}", flush=True)   # the ONLY stdout line
    server.wait_for_termination()

main()
```

Package it as a **`.nplug`**: a zip with exactly two entries at its root — `plugin.json` and `bin/plugin`,
your executable (a `pyinstaller` build, a shebang script, whatever your platform runs). Anything else in the
archive is refused. By convention it is named `<key>_<os>_<arch>.nplug`. A publisher signature, if you have one, travels
BESIDE the file as `<file>.nplug.sig`, over the archive's own bytes — never inside it. The site owner
installs it by uploading the file; the marketplace does not take it (see the top of this document).

---

## 10. If you write an SDK for your language

Please do, and tell us. What the Go SDK provides, and what any SDK should:

- the handshake, the magic cookie, AutoMTLS, and the stdout discipline — all of it hidden
- typed request/response structs per hook, so a renamed field is a compile error rather than a plugin that
  installs and does nothing
- an HTTP client that carries the egress identity on both the request and the CONNECT
- a logger that emits the right JSON
- a test harness with a fake Nilda: the granted capabilities enforced, the settings settable, and the host
  calls recorded — because the half of a plugin worth testing is what it does with the API key
- the shared vocabulary: field types, setting field types, capability names — checked against Nilda's own
  list by a test, so the two cannot drift silently
