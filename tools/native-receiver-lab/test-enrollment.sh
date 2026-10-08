#!/bin/bash
set -euo pipefail
assembler=${1:?Usage: test-enrollment.sh ASSEMBLER_PATH}
root=$(mktemp -d)
ssh-keygen -q -t ed25519 -N "" -f "$root/operator"
cp "$root/operator.pub" "$root/operator_keys"
for source in tenant-red tenant-green tenant-gold; do
  ssh-keygen -q -t ed25519 -N "" -f "$root/$source"
  { printf 'restrict,command="printf harmless-fixture" '; cat "$root/$source.pub"; } > "$root/$source.authorized_key"
done
bash "$assembler" "$root"
[[ "$(ssh-keygen -lf "$root/authorized_keys" | wc -l)" -eq 4 ]]
cp "$root/authorized_keys" "$root/expected"
cp "$root/tenant-red.authorized_key" "$root/duplicate.authorized_key"
if bash "$assembler" "$root"; then
  printf 'Duplicate enrollment unexpectedly accepted\n' >&2
  exit 1
fi
cmp "$root/expected" "$root/authorized_keys"
mv "$root/duplicate.authorized_key" "$root/duplicate.rejected"
chmod 0666 "$root/tenant-green.authorized_key"
if bash "$assembler" "$root"; then
  printf 'Writable enrollment unexpectedly accepted\n' >&2
  exit 1
fi
cmp "$root/expected" "$root/authorized_keys"
printf 'PASS: arbitrary source names, three sources, duplicate rejection, permissions, and atomic preservation\n'