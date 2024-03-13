test_tls_jwt() {
  (
    cd tls2jwt || return
    # Use -buildvcs=false here to prevent git complaining about untrusted directory when tests are run as root.
    go build -tags netgo -v -buildvcs=false ./...
  )

  # Generate a certificate and add it to the trust store
  openssl req -x509 -newkey rsa:4096 -sha384 -keyout "${INCUS_CONF}/jwt-client.key" -nodes -out "${INCUS_CONF}/jwt-client.crt" -days 1 -subj "/CN=test.local"
  incus config trust add-certificate "${INCUS_CONF}/jwt-client.crt" --type=client

  # Get the fingerprint
  FINGERPRINT="$(incus config trust list --format csv --columns cf | grep -F test.local | cut -d, -f2)"

  # Ensure invalid token is not accepted
  JWT="1234567"
  [ "$(curl -k -s -H "Authorization: Bearer ${JWT}" "https://${INCUS_ADDR}/1.0/networks" | jq '.error_code')" -eq 403 ]

  # Ensure valid token is accepted
  JWT="$(./tls2jwt/tls2jwt "${INCUS_CONF}/jwt-client.key" "${INCUS_CONF}/jwt-client.crt" now 60)"
  [ "$(curl -k -s -H "Authorization: Bearer ${JWT}" "https://${INCUS_ADDR}/1.0/networks" | jq '.status_code')" -eq 200 ]

  # Ensure token not valid yet is not accepted
  JWT="$(./tls2jwt/tls2jwt "${INCUS_CONF}/jwt-client.key" "${INCUS_CONF}/jwt-client.crt" "2100-01-01T12:00:00Z" 60)"
  [ "$(curl -k -s -H "Authorization: Bearer ${JWT}" "https://${INCUS_ADDR}/1.0/networks" | jq '.error_code')" -eq 403 ]

  # Ensure token no longer valid is not accepted
  JWT="$(./tls2jwt/tls2jwt "${INCUS_CONF}/jwt-client.key" "${INCUS_CONF}/jwt-client.crt" now -60)"
  [ "$(curl -k -s -H "Authorization: Bearer ${JWT}" "https://${INCUS_ADDR}/1.0/networks" | jq '.error_code')" -eq 403 ]

  # Cleanup
  incus config trust remove "${FINGERPRINT}"
}
