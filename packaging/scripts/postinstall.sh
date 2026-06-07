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

# Worker device/tool groups (least privilege, enumerated).
usermod -aG i2c,kismet,video,plugdev,netdev,czconsole _czconsole 2>/dev/null || true

# Operator user: kali on the graft, pi on Raspberry Pi OS. The files agent runs
# as this user; patch its unit if we're not on the kali default.
OP=kali
id kali >/dev/null 2>&1 || OP=pi
usermod -aG czconsole,kismet "$OP" 2>/dev/null || true
if [ "$OP" != kali ] && [ -f /etc/systemd/system/czconsole-files.service ]; then
  sed -i "s/^User=kali\$/User=$OP/; s#/home/kali#/home/$OP#" \
    /etc/systemd/system/czconsole-files.service
fi

# Kismet's privsep: cap the capture helper so kismet captures without root.
if [ -x /usr/bin/kismet_cap_linux_wifi ]; then
  setcap cap_net_admin,cap_net_raw+eip /usr/bin/kismet_cap_linux_wifi 2>/dev/null || true
fi

# State + config dirs.
install -d -o _czconsole -g _czconsole -m 0750 /var/lib/czconsole /var/lib/czconsole/wardrive
install -d -m 0755 /etc/czconsole/modules.d

# Create the shared socket dir now (it's a tmpfs path; tmpfiles recreates it at
# every boot).
systemd-tmpfiles --create /usr/lib/tmpfiles.d/czconsole.conf 2>/dev/null || true

systemctl daemon-reload 2>/dev/null || true
# Order matters only loosely; the agents come up before/with the worker.
systemctl enable --now czconsole-auth.service czconsole-files.service czconsole.service 2>/dev/null || true

echo "czconsole installed. Login is required by default (edit /etc/czconsole/czconsole.conf to change)."
exit 0
