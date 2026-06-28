#!/bin/sh
# czconsole .deb postinstall — the privsep setup (same intent as
# setup-privsep.sh), run automatically on install.
set -e

# Dedicated, powerless web-worker user (the privilege boundary). No home, no
# shell, no sudo — on Kali, running as 'kali' would be no boundary at all.
id _czconsole >/dev/null 2>&1 || \
  useradd --system --no-create-home --shell /usr/sbin/nologin --user-group _czconsole

# Shared socket group for the worker <-> agents unix sockets.
getent group czconsole >/dev/null || groupadd --system czconsole

# The wardrive module uses the 'kismet' group, which only exists once kismet is
# installed (Kali) — NOT on stock Raspberry Pi OS. The worker unit lists it in
# SupplementaryGroups, so a missing group makes systemd refuse to start it
# (status=216/GROUP) AND makes the usermod below fail atomically. Pre-create it
# (empty, harmless; kismet's own postinst is idempotent over it later).
getent group kismet >/dev/null || groupadd --system kismet

# input/render normally come from udev, but a target may not have them at install
# time. Pre-create (empty, harmless) so the atomic usermod below can't partial-
# fail and silently drop ALL groups. Both are needed by the native LCD frontend:
# input -> /dev/input/event3 (keypad), render -> /dev/dri render node.
getent group input  >/dev/null || groupadd --system input
getent group render >/dev/null || groupadd --system render

# Worker device/tool groups (least privilege, enumerated). All of these now
# exist on both Kali and stock RaspiOS, so the usermod won't partial-fail.
# video+input+render also let the native LCD frontend drive the screen/keypad.
usermod -aG i2c,kismet,video,input,render,plugdev,netdev,czconsole _czconsole || true

# Operator user detection. Prefer $SUDO_USER (the human who ran sudo apt install),
# then fall back to kali (Kali graft) or pi (stock Raspberry Pi OS).
if [ -n "$SUDO_USER" ] && id "$SUDO_USER" >/dev/null 2>&1; then
  OP="$SUDO_USER"
elif id kali >/dev/null 2>&1; then
  OP=kali
else
  OP=pi
fi
usermod -aG czconsole,kismet "$OP" 2>/dev/null || true
# Patch all units that hardcode User=kali / /home/kali when the operator differs.
# (czconsole.service + the kismet unit only carry /home/kali in their wardrive
# bind/-p paths; the User=kali sed is a no-op for them.)
if [ "$OP" != kali ]; then
  for svc in czconsole-files.service czconsole-rtlpower.service czconsole-rtl433.service \
             czconsole-nmap.service czconsole-gobuster.service czconsole.service \
             czconsole-kismet@.service; do
    [ -f /etc/systemd/system/"$svc" ] && \
      sed -i "s/^User=kali\$/User=$OP/; s#/home/kali#/home/$OP#" \
        /etc/systemd/system/"$svc"
  done
fi

# Kismet's privsep: cap the capture helper so kismet captures without root.
if [ -x /usr/bin/kismet_cap_linux_wifi ]; then
  setcap cap_net_admin,cap_net_raw+eip /usr/bin/kismet_cap_linux_wifi 2>/dev/null || true
fi

# Native LCD frontend: register the APPLaunch tile + placeholder icon, and let the
# console operator launch the LCD binary as the deprivileged _czconsole worker.
# APPLaunch exists only on the M5 graft (not stock RaspiOS), so this is gated on
# its presence. APPLaunch hardcodes launching tiles as the operator (kali/pi);
# the tile's Exec does `sudo -n -u _czconsole`, so the UI runs as the worker and
# the wardrive polkit grant (keyed on _czconsole) applies. The sudoers rule below
# scopes that to THIS binary only — nothing broader.
if [ -d /usr/share/APPLaunch/applications ]; then
  install -m 0644 /usr/local/lib/czconsole/applaunch/czconsole-lcd.desktop \
    /usr/share/APPLaunch/applications/czconsole-lcd.desktop
  install -d -m 0755 /usr/share/APPLaunch/share/images
  install -m 0644 \
    /usr/local/lib/czconsole/applaunch/czconsole-lcd.png \
    /usr/local/lib/czconsole/applaunch/czconsole-lcd_100.png \
    /usr/local/lib/czconsole/applaunch/czconsole-lcd_80.png \
    /usr/share/APPLaunch/share/images/
  printf '%s ALL=(_czconsole) NOPASSWD: /usr/local/bin/czconsole-lcd\n' "$OP" \
    > /etc/sudoers.d/czconsole-lcd
  chmod 0440 /etc/sudoers.d/czconsole-lcd
fi

# rfheatmap — fetched from GitHub releases (arm64 binary, no build step).
RFHM_URL="https://github.com/n0xa/rfheatmap/releases/download/v0.1.0/rfheatmap-linux-arm64"
if command -v curl >/dev/null 2>&1; then
  curl -fsSL -o /usr/local/bin/rfheatmap "$RFHM_URL" && chmod 755 /usr/local/bin/rfheatmap || \
    echo "warn: rfheatmap download failed — SDR heatmap generation unavailable"
else
  echo "warn: curl not found — install rfheatmap manually to /usr/local/bin/rfheatmap"
fi

# SDR wrapper scripts — installed from the .deb contents block.
# /usr/local/lib/czconsole/ already contains them; just ensure the dir exists.
install -d /usr/local/lib/czconsole

# State + config dirs.
install -d -o _czconsole -g _czconsole -m 0750 /var/lib/czconsole /var/lib/czconsole/wardrive /var/lib/czconsole/sdr
install -d -m 0755 /etc/czconsole/modules.d

# ── Tool-output dirs in the operator's home (the shared recon-tool convention) ─
# Every capture/recon tool writes timestamped files under ~/<tool>/ so they're
# browsable via the Files module next to ~/SDR. Each dir is setgid to the shared
# 'czconsole' group + group-writable (2775): the tool (kismet as _czconsole;
# nmap as the operator) writes captures the operator owns, and — crucially — the
# group-czconsole ownership makes them readable by the deprivileged frontends.
# Must exist before the worker restarts below (its BindReadOnlyPaths reference
# them).
if [ -d "/home/$OP" ]; then
  install -d -o "$OP" -g czconsole -m 2775 "/home/$OP/Wardriving"
  install -d -o "$OP" -g czconsole -m 2775 "/home/$OP/nmap"
  install -d -o "$OP" -g czconsole -m 2775 "/home/$OP/gobuster"

  # ── Why this ACL exists (read before "fixing" it) ───────────────────────────
  # Two deprivileged consumers read these tool dirs, and they reach them by
  # DIFFERENT mechanisms because they run in different contexts:
  #
  #   1. The web worker (czconsole.service) is a SANDBOXED systemd unit with
  #      ProtectHome=tmpfs — it can't see /home at all, so it gets explicit
  #      read-only BIND MOUNTS of each tool dir in its private namespace.
  #
  #   2. The native LCD frontend (czconsole-lcd) is launched by APPLaunch via
  #      plain `sudo -u _czconsole` — NO systemd sandbox, so NO bind mount. To
  #      read ~/nmap it must TRAVERSE /home/$OP, which is mode 0700 (owner-only).
  #      That traversal is the wall (the same one that killed the kismet symlink
  #      idea): without help, _czconsole cannot enter the home at all.
  #
  # The fix is a single POSIX ACL granting the czconsole group EXECUTE (traverse)
  # — and ONLY execute, not read — on the home dir. That lets _czconsole pass
  # THROUGH /home/$OP to the explicitly-shared, group-czconsole tool dirs, while
  # the home itself stays unlistable (no 'r'): the group can't enumerate the
  # home, and private dotdirs (~/.ssh etc., mode 0700) stay sealed because
  # traversal still stops at THEIR permission bits. So the only things reachable
  # are exactly the dirs we deliberately made group-accessible above.
  #
  # Alternatives rejected: `chmod o+x /home/$OP` (world-traverse — broader than
  # needed); relocating output to /var/lib (loses Files browsing + operator
  # ownership); wrapping the LCD launch in `systemd-run` with binds (heavier,
  # fb/keypad access gets fiddlier). See docs/native-lcd.md for the full writeup.
  if command -v setfacl >/dev/null 2>&1; then
    setfacl -m g:czconsole:x "/home/$OP" || \
      echo "warn: setfacl on /home/$OP failed — the LCD frontend won't read ~/nmap"
  else
    echo "warn: setfacl not found (install 'acl') — the LCD frontend can't traverse /home/$OP to read tool output"
  fi
fi

# Create the shared socket dir now (it's a tmpfs path; tmpfiles recreates it at
# every boot).
systemd-tmpfiles --create /usr/lib/tmpfiles.d/czconsole.conf 2>/dev/null || true

systemctl daemon-reload 2>/dev/null || true
# Enable (boot) then restart. `enable --now` does NOT restart an already-running
# service, so on UPGRADE the old binary keeps running and the new one never loads
# (the HDMI module / wardrive-status fix silently wouldn't appear). `restart`
# starts them on first install and swaps in the new binary on upgrade.
# Order matters only loosely; the agents come up before/with the worker.
systemctl enable czconsole-auth.service czconsole-files.service czconsole.service 2>/dev/null || true
systemctl restart czconsole-auth.service czconsole-files.service czconsole.service 2>/dev/null || true

echo "czconsole installed. Login is required by default (edit /etc/czconsole/czconsole.conf to change)."
exit 0
