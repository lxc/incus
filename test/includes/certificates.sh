cert_fingerprint() {
  openssl x509 -noout -fingerprint -sha256 -in "${1}" | sed 's/.*=//; s/://g; s/\(.*\)/\L\1/'
}

# Generate a short lived ecdsa (self-signed) cert and key
# using the NIST curve P-384 and SHA384 hash for signature
# to match what Incus generates by default.
gen_cert_and_key() {
    key_file="${1}"
    crt_file="${2}"
    cn="${3}"
    openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:secp384r1 -sha384 -keyout "${key_file}" -nodes -out "${crt_file}" -days 1 -subj "/CN=${cn}"
}
