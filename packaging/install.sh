#!/bin/sh
# Manual installer for czconsole (the .deb does all this automatically; this is
# for hacking on a device directly). Run as root from the packaging/ dir, with
# the cross-built binary passed as the first argument.
#
#   CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o czconsole ./cmd/czconsole
#   scp czconsole -r packaging kali@cz:/tmp/cz/
#   ssh kali@cz 'cd /tmp/cz/packaging && sudo sh install.sh /tmp/cz/czconsole'
set -e

BIN="${1:-./czconsole}"
HERE="$(cd "$(dirname "$0")" && pwd)"

[ -f "$BIN" ] || { echo "binary not found: $BIN"; exit 1; }

# Binary.
install -m755 "$BIN" /usr/local/bin/czconsole

# Units.
for u in czconsole.service czconsole-files.service czconsole-auth.service czconsole-kismet@.service \
         czconsole-hdmi-enable.service czconsole-hdmi-disable.service; do
  install -m644 "$HERE/$u" /etc/systemd/system/"$u"
done

# Polkit rules (kismet capture, hdmi/lightdm control), tmpfiles (shared socket dir).
install -d /etc/polkit-1/rules.d
install -m644 "$HERE/50-czconsole-kismet.rules" /etc/polkit-1/rules.d/50-czconsole-kismet.rules
install -m644 "$HERE/55-czconsole-hdmi.rules"   /etc/polkit-1/rules.d/55-czconsole-hdmi.rules
install -m644 "$HERE/tmpfiles-czconsole.conf"   /usr/lib/tmpfiles.d/czconsole.conf

# Config (don't clobber existing edits).
install -d /etc/czconsole/modules.d /etc/pam.d
[ -f /etc/pam.d/czconsole ]            || install -m644 "$HERE/pam/czconsole"   /etc/pam.d/czconsole
[ -f /etc/czconsole/czconsole.conf ]   || install -m644 "$HERE/czconsole.conf"  /etc/czconsole/czconsole.conf

# Users/groups/caps/state dirs.
sh "$HERE/setup-privsep.sh"

# PAM login needs pamtester.
command -v pamtester >/dev/null 2>&1 || apt-get install -y pamtester || \
  echo "warn: install pamtester for login to work"

systemd-tmpfiles --create /usr/lib/tmpfiles.d/czconsole.conf 2>/dev/null || true
systemctl daemon-reload
systemctl enable --now czconsole-auth.service czconsole-files.service czconsole.service

# Report the URL using the *actual* listen port. The effective port is the last
# --listen on the command line: the unit's ExecStart sets one, and the env-file
# $CZCONSOLE_OPTS (appended after it) may override — so the conf wins if present.
PORT=8080
for f in /etc/czconsole/czconsole.conf /etc/systemd/system/czconsole.service; do
  v=$(grep -hoE -- '--listen[ =][^ ]+' "$f" 2>/dev/null | tail -1)
  if [ -n "$v" ]; then PORT="${v##*:}"; break; fi
done

echo "czconsole installed (login required by default). Reachable at:"
ip -4 -o addr show scope global 2>/dev/null \
  | awk -v p="$PORT" '{split($4,a,"/"); print "  http://"a[1]":"p}'
