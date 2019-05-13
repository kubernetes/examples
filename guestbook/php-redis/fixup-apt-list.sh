#!/usr/bin/env bash

DEB_ARCH=$(dpkg --print-architecture)

# http://security.debian.org/debian-security/dists/jessie/updates/InRelease is missing
# entries for some platforms, so we just remove the last line in sources.list in
# /etc/apt/sources.list which is "deb http://deb.debian.org/debian jessie-updates main"

case ${DEB_ARCH} in
    arm64|ppc64el|s390x)
        sed -i '/security/d' /etc/apt/sources.list
        ;;
esac

exit 0
