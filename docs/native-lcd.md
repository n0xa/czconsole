# czconsole-lcd — native on-screen frontend (handoff / architecture)

> **Status:** foundation built and deployed; UI navigates on hardware. The
> shared **`internal/wardrive.Core`** drives **both** frontends — the LCD HUD and
> the web module — verified on hardware (LCD live stats + polkit Start/Stop as
> `_czconsole`; the web API returns its exact prior JSON, now core-sourced,
> including the live AP feed). The cgroup check is extracted into **`internal/unit`**
> (shared by the core + sdr/hdmi). Wardrive is now single-source-of-truth.
> Everything lives on branch **`native-lcd`**, **uncommitted** until MVP-complete.
> Tracked by Deck card **#273** (Nerd Projects → In Progress), which supersedes
> the retired C++/LVGL app (#271).

## TL;DR for a fresh agent

`czconsole-lcd` is a **second, single-purpose Go binary** in this repo that draws
the CardputerZero's 320×170 LCD and reads its keypad. It's launched as an
**APPLaunch tile** and shares czconsole's `internal/*` packages (wardrive,
sysinfo, fb) with the always-running web worker — **one backend, two frontends
(web + LCD)**. It is *not* a separate project; do all work here in
`/home/axon/source/czconsole` on branch `native-lcd`.

## Build / deploy / test

```bash
# Build (pure Go, no cgo — cross-compiles from any host)
cd /home/axon/source/czconsole
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o czconsole-lcd ./cmd/czconsole-lcd

# Deploy to the device
scp czconsole-lcd kali@192.168.1.126:/tmp/
ssh kali@192.168.1.126 'sudo install -m755 /tmp/czconsole-lcd /usr/local/bin/czconsole-lcd'

# Test path A — from the device: open APPLaunch, select the "Kali Tools" tile.
# Test path B — headless over SSH (frees the framebuffer first):
ssh kali@192.168.1.126 '
  sudo systemctl stop APPLaunch
  sudo -u _czconsole timeout 6 /usr/local/bin/czconsole-lcd   # runs as the worker, like the tile
  sudo systemctl start APPLaunch'
```

- The binary renders to the framebuffer (no stdout UI); errors go to stderr/log.
- `timeout … ` returning **124** = it ran the whole window without crashing.

### Device facts
- Host: **`kali@192.168.1.126`**, passwordless sudo. CardputerZero / Kali graft.
- Worker user: **`_czconsole`** (uid 999), in groups `video input render kismet
  netdev plugdev i2c czconsole`. Owns `/var/lib/czconsole/wardrive`.
- LCD: st7789 fbdev (use `fb.FindLCD()`, **don't assume /dev/fb0** — index varies).
- Keypad: `tca8418c` at `/dev/input/event3` (evdev).
- Kismet capture: `czconsole-kismet@wlan1.service` (start/stop via systemctl;
  polkit-authorized for `_czconsole`).

## File map

```
cmd/czconsole-lcd/main.go      entrypoint: opens display+keypad, runs the app
internal/fb/display.go         framebuffer WRITER (mmap RW, RGBA→RGB565) — NEW
internal/fb/fb.go              existing mirror READER (shared ioctls/FindLCD)
internal/unit/cgroup.go        shared fork-free systemd unit liveness check — NEW
internal/wardrive/core.go      shared privilege-free Core (Status/Start/Stop/Interfaces/Password/RecentAPs/LatestLog) — NEW
internal/modules/wardrive.go   thin HTTP shell over wardrive.Core (+ web-only export)
internal/lcd/
  input.go                     evdev reader; finds keypad; maps arrows + f/z/x/c
  draw.go                      Canvas: rects/borders/text (embedded Go Mono)
  theme.go                     green-on-black palette + chrome (titlebar/footer/pill/card)
  app.go                       Screen interface (Draw/Key) + stack + ~5Hz loop; Close() on pop
  menu.go                      root "KALI TOOLS" list
  wardrive.go                  wardrive HUD — LIVE via internal/wardrive.Core (bg poller)
  placeholder.go               "coming soon" for unwired tools
packaging/applaunch/           .desktop tile + placeholder icons (>_ motif)
```

Shared: `internal/wardrive.Core` (capture orchestration; the LCD wraps it now,
the web module will too), `internal/sysinfo` (`MonCapIface`).

## Architecture & key decisions

- **Immediate mode, not a widget toolkit.** Each `Screen` has `Draw(canvas)` and
  `Key(k)`; the loop redraws on key + on a ticker. The UI is text + boxes + (soon)
  a sparkline — LVGL's touch/widget machinery buys nothing on a keyboard-only
  device, and pure Go cross-compiles trivially.
- **Framebuffer ownership.** APPLaunch *cedes* the framebuffer to whichever
  foreground program it launches and resumes on exit — so the tile owns
  `/dev/fb0` while running. (For headless SSH testing, stop APPLaunch first so the
  fb is free.)
- **Privilege separation is preserved — the LCD runs as `_czconsole`.** This is
  the load-bearing detail (see Gotchas). The polkit rule keys on
  `subject.user == "_czconsole"`, so running as that user means Start/Stop of the
  kismet unit is authorized with **no sudo, no root**, exactly like the web
  worker. The network worker's deprivileging is untouched.

## Two privileged subsystems (read this before touching them)

These are the two pieces of "advanced systems architecture" in the recon tools.
Both look like black magic without the rationale; both are deliberate and the
obvious-looking simplifications are wrong. If you're tempted to "clean one up,"
the reasons it's shaped this way are below.

### 1. Capabilities: unit-scoped, never `setcap` on the shared binary

Recon tools that need raw sockets (nmap SYN/UDP/OS-detect; tcpdump later) get
**`CAP_NET_RAW` from their per-tool systemd unit's `AmbientCapabilities=`**, not
from file capabilities on `/usr/bin/nmap`.

- **Why not `setcap cap_net_raw+eip /usr/bin/nmap`:** it's a *shared distro
  binary*, so file caps apply to **every** nmap invocation on the box (a
  system-wide raw-socket grant to anyone who runs nmap), **and** `dpkg` strips
  file caps on the next nmap upgrade — silently breaking the tool.
- **The model:** the unit runs as the operator (`User=kali`) with
  `AmbientCapabilities=CAP_NET_RAW` + `CapabilityBoundingSet=CAP_NET_RAW` +
  `NoNewPrivileges=no`. The cap is scoped to that one single-purpose unit,
  least-privilege, and upgrade-proof. `CAP_NET_ADMIN` is *not* granted — that's
  interface administration (kismet needs it for monitor mode; nmap doesn't).
- **`--privileged` is mandatory.** nmap decides "am I privileged?" by `euid==0`.
  Running as the non-root operator it would *downgrade to a TCP connect scan*
  even with the cap present. The wrapper passes `--privileged` so nmap uses the
  raw socket the cap permits. (Verified on hardware: as `kali`, SYN scan fails
  without the cap, succeeds with `AmbientCapabilities=CAP_NET_RAW` + `--privileged`.)
- **Why kismet *also* uses `setcap`:** the kismet unit grants ambient caps to the
  kismet *process*, but kismet **spawns a separate capture helper**
  (`kismet_cap_linux_wifi`) outside any unit — so that helper binary is
  `setcap`'d directly (`cap_net_admin,cap_net_raw+eip`, in the postinstall). nmap
  is a single process, so the unit cap suffices and no binary is touched.

### 2. Reading operator-home tool output from two deprivileged contexts

Every recon tool writes `~/<tool>/` (e.g. `~/nmap`, `~/Wardriving`), owned
`operator:czconsole`, mode **2775** (setgid → new files inherit group
`czconsole` and are group-readable). Both frontends are deprivileged and must
*read* that output, but `/home/<op>` is `0700` (owner-only) — so neither can just
open the path. They get in by **two different mechanisms because they run in two
different contexts**, and that asymmetry is the thing to understand:

| consumer | context | how it reads `~/nmap` |
|---|---|---|
| **web worker** (`czconsole.service`) | sandboxed systemd unit, `ProtectHome=tmpfs` (can't see `/home` at all) | explicit **`BindReadOnlyPaths=`** of each tool dir into its private namespace |
| **LCD** (`czconsole-lcd`) | launched by APPLaunch via plain `sudo -u _czconsole`, **no systemd sandbox → no bind** | a **POSIX traverse ACL** on the home: `setfacl -m g:czconsole:x /home/<op>` |

The ACL grants the `czconsole` group **execute only — not read** — on the home
dir. So `_czconsole` can *traverse through* `/home/<op>` to the explicitly-shared,
group-`czconsole` tool dirs, but **cannot list the home**, and private dotdirs
(`~/.ssh`, mode 0700) stay sealed because traversal still stops at *their*
permission bits. The only things reachable are exactly the dirs we deliberately
made group-accessible. This is the same `0700` traversal wall that killed the
kismet-log *symlink* idea earlier — there's no shortcut around it; you either
bind it (sandbox) or ACL-traverse to it (no sandbox).

- **Rejected alternatives:** `chmod o+x /home/<op>` (world-traverse — broader than
  needed); relocating output to `/var/lib/czconsole/<tool>` (loses Files
  browsing + operator ownership of their own scans); wrapping the LCD launch in
  `systemd-run` with binds (keeps home perms untouched but makes the
  framebuffer/keypad access fiddly and adds a per-tool bind list to maintain).
- **Dependency:** `setfacl` (the `acl` package — now a `Depends:`). The
  postinstall guards on its presence and warns if missing.
- **Invariant for future tools:** create `~/<tool>` as `operator:czconsole 2775`
  in the postinstall, add a `BindReadOnlyPaths=-/home/kali/<tool>` to
  `czconsole.service`, and you're done — the single home ACL already covers
  traversal for every tool dir.

## Wardrive data model (decided; this is what to implement)

`running` = fork-free **cgroup** read (reuse `unitProcs`/`unitCgroupActive`).
Live stats come from **kismet's REST API** while running (NOT the sqlite — that's
export-only via `kismetdb_to_*`). Creds: read user/pass from
`/var/lib/czconsole/wardrive/.kismet/kismet_httpd.conf` (owned by `_czconsole`).

- **devices** ← `GET /system/status.json` → `kismet.system.devices.count`
- **uptime** ← same doc: `kismet.system.timestamp.sec − kismet.system.timestamp.start_sec`
  (kismet's own clock both sides → skew-free; precise; identical across frontends)
- **APs** ← `GET /devices/views/all_views.json` → size of the
  `phydot11_accesspoints` view (`kismet.devices.view.id`/`.size`)
- **clients** = devices − aps; **new/min** = devices / (uptime/60)
- **GPS** ← `GET /gps/location.json` → `kismet.common.location.geopoint:[lon,lat]`,
  `[0,0]` = no fix
- **start/stop** = `systemctl start --no-block | stop czconsole-kismet@<iface>`
- **interface filter** = `sysinfo.MonCapIface` driver allowlist (excludes onboard
  brcmfmac; the device's wlan1 driver is `rtl88XXau`). Lock the iface selector
  while capturing.
- **Password policy (decided):** drop the per-run `randHex` for a **static**
  `httpd_password`, and add a **reveal-password key** on the LCD (physical
  possession = authorization).
- **Capture logs (decided + done):** the `.kismet` files go to the **operator's
  `~/Wardriving/`** (next to `~/SDR/`, browsable via the Files module), via the
  kismet unit's `-p`. Split: config/creds/db stay in `_czconsole`'s `/var/lib`
  homedir (Core territory); only the human-facing logs go to `/home`. Mechanics:
  the dir is `<op>:czconsole 2775` (setgid) so kismet (running `_czconsole`, in
  the `czconsole` group) writes captures the operator owns; the kismet unit binds
  it read-write (`ProtectHome=tmpfs` + `BindPaths`), and the **worker** sees it
  **read-only** (`BindReadOnlyPaths`) just for export. Export reads
  `DefaultFilesRoot()/Wardriving`.
  - **GOTCHA:** a manual per-interface drop-in
    (`/etc/systemd/system/czconsole-kismet@wlan1.service.d/channels.conf`, NOT
    packaged) does a full `ExecStart=` override to pin channels — and it also
    carries `-p`, which **shadows the base unit's path**. Such drop-ins must keep
    `-p` in sync (or we move `log_prefix` into kismet config so they needn't).

## Current state vs. next steps

Done: fb writer, evdev input, draw/theme layer, app loop, KALI TOOLS menu,
deployed + launching as `_czconsole`, packaging wired (binary, tile+icon, groups,
sudoers, removal cleanup). **Shared `internal/wardrive.Core` built** (Status with
the timestamp uptime math + static password, Start/Stop via systemctl+polkit,
MonCapIface interface filter). **LCD HUD wired live** to it via a background
poller (kismet REST can block, so it's off the UI thread); Enter = Start/Stop.

**Next, in order:**
1. **LCD interface picker** (currently `Start` uses the single monitor NIC) and a
   **reveal-password key** (`Core.Password()` is ready) — both need a couple new
   key mappings in `input.go`.
2. Real "Kali Tools" icon; then the MVP tool modules (nmap, gobuster, tcpdump,
   ssh-tunnels).

Done: shared `wardrive.Core` drives both frontends; `internal/unit` extracted;
web JSON contract preserved + verified.

Heads-up: the LCD redraws ~5 Hz but the wardrive poller hits kismet every **2 s**
(don't fetch in `Draw`). Start/Stop runs in a goroutine off the key handler.

## Gotchas / lessons (these cost time to discover)

1. **APPLaunch launches tiles as the operator `kali` with `keep_root=0`,
   hardcoded.** It is NOT overridable via a `.desktop` `run_as_user=` field — we
   tried; it's ignored. A `setpriv`-in-a-wrapper approach **fails**
   (`setresuid: Operation not permitted`) because APPLaunch already dropped root
   before exec. The working answer: the tile's `Exec=sudo -n -u _czconsole
   /usr/local/bin/czconsole-lcd` — a one-time identity transition at launch using
   the operator's passwordless sudo. Packaged as a scoped
   `/etc/sudoers.d/czconsole-lcd` (`<operator> ALL=(_czconsole) NOPASSWD: …`).
2. **`_czconsole` needs `input` (and we also granted `render`) beyond the `video`
   it already had.** Without `input` it can't open the keypad. The package
   pre-creates these groups before the atomic `usermod` (a missing group makes the
   whole `usermod -aG` fail and silently drop ALL groups).
3. **Run as `_czconsole`, not root** — required for the polkit grant + cred
   ownership. Running as root (or kali) would work-but-wrong: root over-privileges
   the console; kali isn't in the polkit allowlist.
4. **kismet REST quirks (this build):** there is NO `/devices/count` endpoint
   (count is in `status.json`); GPS is `geopoint`, not `kismet.gps.last.*`; the
   precise start time is `…timestamp.start_sec`, not `…status.start_time`.
5. **Build is pure Go / no cgo.** Keep it that way (the cross-compile story is the
   whole point vs. the retired C++/LVGL/BSP-sysroot pipeline).

## Packaging (already wired in nfpm + scripts)

- `nfpm.yaml`: builds + ships `czconsole-lcd` → `/usr/local/bin/`; stages the
  tile/icons under `/usr/local/lib/czconsole/applaunch/`.
- `postinstall.sh`: pre-creates + grants `input`/`render`; **gated on
  `/usr/share/APPLaunch` existing** (M5 graft only, not stock RaspiOS), installs
  the tile + icons and writes the operator-scoped sudoers drop-in.
- `postremove.sh`: removes the tile/icons/sudoers.
- Icon is a placeholder (`>_`); swap in the real "Kali Tools" logo at
  `packaging/applaunch/czconsole-lcd{,_100,_80}.png`.
