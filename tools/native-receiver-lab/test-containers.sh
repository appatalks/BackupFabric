#!/bin/bash
set -euo pipefail
first_source=${1:?Usage: test-containers.sh SOURCE_ONE SOURCE_TWO}
second_source=${2:?Usage: test-containers.sh SOURCE_ONE SOURCE_TWO}
[[ "$first_source" =~ ^[a-z][a-z0-9-]{0,62}$ && "$second_source" =~ ^[a-z][a-z0-9-]{0,62}$ && "$first_source" != "$second_source" ]]
broker_path=${BACKUPFABRIC_BROKER:-/usr/local/libexec/backupfabric-native-lab}
config=${BACKUPFABRIC_RECEIVER_CONFIG:?Set BACKUPFABRIC_RECEIVER_CONFIG to the root-owned route JSON}

broker() {
  sudo -n "$broker_path" -config "$config" "$1" "$2"
}

fixture="/data/backup/lab-fixtures/$(date -u +%Y%m%dT%H%M%S)-$$"
for source in "$first_source" "$second_source"; do
  broker "$source" "set -eu; test -d /data/backup; mountpoint -q /data/backup; test ! -e /data/user; test ! -S /var/run/docker.sock; test ! -w /usr/local/bin; test \"\$(id -u)\" -ne 0; grep -Eq '^CapEff:[[:space:]]+0+$' /proc/self/status; ghe-version"
  broker "$source" "set -eu; mkdir -p '$fixture/20261008T010203'; printf '%s\n' '$source' > '$fixture/20261008T010203/object'; ln '$fixture/20261008T010203/object' '$fixture/20261008T010203/hardlink'; ln -s object '$fixture/20261008T010203/symlink'; ln -s 20261008T010203 '$fixture/current'"
  content=$(broker "$source" "cat '$fixture/20261008T010203/object'")
  linked=$(broker "$source" "cat '$fixture/20261008T010203/symlink'")
  [[ "$content" == "$source" && "$linked" == "$source" ]]
  inodes=$(broker "$source" "stat -c %i '$fixture/20261008T010203/object' '$fixture/20261008T010203/hardlink'")
  [[ "$(printf '%s\n' "$inodes" | sort -u | wc -l)" -eq 1 ]]
  [[ "$(broker "$source" "readlink '$fixture/current'")" == 20261008T010203 ]]
done

broker "$first_source" "printf '%s\n' '$first_source' > '$fixture/concurrent'" &
first_pid=$!
broker "$second_source" "printf '%s\n' '$second_source' > '$fixture/concurrent'" &
second_pid=$!
wait "$first_pid"
wait "$second_pid"
[[ "$(broker "$first_source" "cat '$fixture/concurrent'")" == "$first_source" ]]
[[ "$(broker "$second_source" "cat '$fixture/concurrent'")" == "$second_source" ]]

for rejected in unknown "../$first_source" "--$first_source"; do
  if broker "$rejected" "printf '%s\n' harmless-probe"; then
    printf 'Unexpected route acceptance: %s\n' "$rejected" >&2
    exit 1
  fi
done
if broker "$first_source" ""; then
  printf 'Unexpected interactive command acceptance\n' >&2
  exit 1
fi

printf 'PASS: isolated identical paths, links, concurrent writes, restricted runtime, and invalid routes\n'
printf 'Synthetic fixtures retained at %s in each receiver\n' "$fixture"