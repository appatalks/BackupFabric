#!/bin/sh
set -eu
backup=${1:?Usage: ssh-rollback.sh BACKUP_CONFIG ACTIVE_CONFIG [SERVICE]}
active=${2:?Usage: ssh-rollback.sh BACKUP_CONFIG ACTIVE_CONFIG [SERVICE]}
service=${3:-ssh.service}
case "$backup" in /*) ;; *) exit 2;; esac
case "$active" in /*) ;; *) exit 2;; esac
/usr/sbin/sshd -t -f "$backup"
cp -p "$backup" "$active"
/usr/sbin/sshd -t -f "$active"
kill -HUP "$(systemctl show "$service" -p MainPID --value)"