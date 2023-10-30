#!/bin/sh -eu

f="internal/server/auth/driver_openfga_model.go"

openfga_model_hash() {
  md5sum "${f}" | cut -f1 -d" "
}

echo "Checking that ${f} is up to date..."

# make sure the YAML is up to date
hash1="$(openfga_model_hash)"
mv "${f}" "${f}.bak"
make update-openfga -s
hash2="$(openfga_model_hash)"
mv "${f}.bak" "${f}"
if [ "${hash1}" != "${hash2}" ]; then
  echo "==> Please update the ${f} file in your commit (make update-openfga)"
  exit 1
fi
