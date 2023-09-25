cert_fingerprint() {
  openssl x509 -noout -fingerprint -sha256 -in "${1}" | sed 's/.*=//; s/://g; s/\(.*\)/\L\1/'
}
