#!/bin/sh
# Idempotent privsep setup for czconsole. Creates the dedicated worker user,
# the shared socket group, the device/tool group memberships, and gives
# kismet's capture helper its caps so it captures WITHOUT root. Run as root.
#
#   sudo FILES_USER=kali sh setup-privsep.sh
set -e

FILES_USER="${FILES_USER:-kali}"

# Dedicated unprivileged web-worker user — no home, no shell, NO sudo. This is
# the privilege boundary: on Kali, running as 'kali' is no boundary (kali has
# NOPASSWD: ALL), so the network-facing worker gets its own powerless user.
id _czconsole >/dev/null 2>&1 || \
  useradd --system --no-create-home --shell /usr/sbin/nologin --user-group _czconsole

# Shared group for the worker <-> files-agent unix socket.
getent group czconsole >/dev/null || groupadd --system czconsole

# Worker group memberships (least privilege, enumerated):
#   i2c     -> read /dev/i2c-1 (battery)
#   kismet  -> invoke kismet's setcap'd capture helper
#   video   -> /dev/fb0 (LCD mirror)
#   plugdev -> USB SDR / Wi-Fi adapters
#   netdev  -> network state
#   czconsole -> connect the files-agent socket
usermod -aG i2c,kismet,video,plugdev,netdev,czconsole _czconsole

# Operator shares the socket group (to be reached by the worker) and kismet.
usermod -aG czconsole,kismet "$FILES_USER" 2>/dev/null || true

# Kismet's privsep model: the privileged work is in the capture helper, NOT the
# main binary. Cap the helper so kismet captures unprivileged — this is the fix
# for "monitor device not recognized" when running kismet as a normal user.
if [ -x /usr/bin/kismet_cap_linux_wifi ]; then
  setcap cap_net_admin,cap_net_raw+eip /usr/bin/kismet_cap_linux_wifi || \
    echo "warn: setcap on kismet_cap_linux_wifi failed (capture may need root)"
fi

# Wardrive log/state dir, owned by the worker.
install -d -o _czconsole -g _czconsole -m 0750 /var/lib/czconsole
install -d -o _czconsole -g _czconsole -m 0750 /var/lib/czconsole/wardrive

# PAM login layer: the auth agent execs pamtester. Ensure it's present (it's in
# kali-rolling/main and Debian trixie/main). The actual PAM service file
# (/etc/pam.d/czconsole) and units are placed by install.sh / the .deb.
if ! command -v pamtester >/dev/null 2>&1; then
  echo "note: pamtester not installed — run: apt-get install -y pamtester"
fi

echo "privsep setup complete (worker=_czconsole, operator=$FILES_USER)"
