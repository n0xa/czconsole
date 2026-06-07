#!/bin/sh
# Clean up after removal. On purge, drop the dedicated worker user; leave the
# czconsole group and operator memberships alone (cheap, possibly shared).
set -e
systemctl daemon-reload 2>/dev/null || true
if [ "$1" = purge ]; then
  userdel _czconsole 2>/dev/null || true
  rm -rf /var/lib/czconsole 2>/dev/null || true
fi
exit 0
