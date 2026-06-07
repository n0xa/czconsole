#!/bin/sh
# Stop and disable the czconsole units before files are removed.
set -e
if [ "$1" = remove ] || [ "$1" = purge ] || [ "$1" = "0" ]; then
  systemctl disable --now \
    czconsole.service czconsole-files.service czconsole-auth.service 2>/dev/null || true
fi
exit 0
