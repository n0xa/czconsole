# czconsole Module Spec (draft)

A module adds a capability to the field console. The core renders its UI
**generically from the manifest** — you declare *what* it does; the core draws
the buttons, status fields, and log pane. Ship custom HTML only if you want a
richer view.

## External module layout

```
/etc/czconsole/modules.d/
  mymodule/
    module.toml      # required: metadata + actions
    index.html       # optional: custom UI (otherwise generic UI is generated)
    run.sh           # whatever your actions invoke
```

## module.toml

```toml
name        = "Wardrive"
icon        = "radar"
description = "Kismet + GPS site survey"

# The module is shown 'available' only if every requirement resolves on the
# running system. This is what makes one binary work on stock RaspiOS and Kali.
[requires]
binaries = ["kismet", "gpspipe"]   # each must be on PATH
files    = []                      # each path must exist
any_interface = "monitor"          # >=1 monitor-capable adapter (checked at run time)

# Actions become UI. Types:
#   service  – long-running; core gives Start/Stop + live log streaming
#   poll     – core reads `source` on `interval` and renders `render` fields
#   command  – one-shot; output streamed to a log pane
#   geojson  – core fetches `source` and draws pins on a map

[[action]]
id    = "start"
label = "Start Wardrive"
type  = "service"
start = "kismet -c {iface} --no-ncurses"
# {iface} is filled from a picker the core renders:
iface = { from = "interface_picker", filter = "monitor" }

[[action]]
id       = "status"
type     = "poll"
interval = "2s"
source   = "http://localhost:2501/devices/views/all/devices.json"  # kismet REST
render   = ["ap_count", "client_count", "last_ssid"]

[[action]]
id     = "map"
type   = "geojson"
source = "czconsole://wardrive/aps.geojson"   # core helper queries the kismetdb
```

## Design rules

- **Declare requirements honestly.** Availability is computed from them; a module
  that needs `kismet` must say so, so it greys out cleanly on stock RaspiOS.
- **No frontend required.** The generic UI covers buttons + status + logs + maps.
- **Long-running things are `service` actions** so the core can supervise
  start/stop and stream logs — don't fork daemons yourself.
- **Keep secrets out of the manifest.** Tokens/keys belong in env or a sibling
  file your `run.sh` reads.

> Status: draft. The schema is finalized alongside the first bundled module
> (`wardrive`); fields may shift until then.
