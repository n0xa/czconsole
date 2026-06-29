#!/bin/sh
# Clean up after removal. On purge, drop the dedicated worker user; leave the
# czconsole group and operator memberships alone (cheap, possibly shared).
set -e
systemctl daemon-reload 2>/dev/null || true

# Remove the native LCD tile/icons/sudoers we registered in postinstall (only
# present where APPLaunch was).
rm -f /usr/share/APPLaunch/applications/czconsole-lcd.desktop \
      /usr/share/APPLaunch/share/images/czconsole-lcd.png \
      /usr/share/APPLaunch/share/images/czconsole-lcd_100.png \
      /usr/share/APPLaunch/share/images/czconsole-lcd_80.png \
      /etc/sudoers.d/czconsole-lcd 2>/dev/null || true

if [ "$1" = purge ]; then
  userdel _czconsole 2>/dev/null || true
  rm -rf /var/lib/czconsole 2>/dev/null || true
fi
exit 0
