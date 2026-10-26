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
    [ "$(curl -k -s "https://${INCUS_ADDR}/1.0/networks?access_token=${JWT}" | jq '.error_code')" -eq 403 ]

    # Ensure valid token is accepted
    JWT="$(./tls2jwt/tls2jwt "${INCUS_CONF}/jwt-client.crt" "${INCUS_CONF}/jwt-client.key" now 60)"
    [ "$(curl -k -s -H "Authorization: Bearer ${JWT}" "https://${INCUS_ADDR}/1.0/networks" | jq '.status_code')" -eq 200 ]
    [ "$(curl -k -s "https://${INCUS_ADDR}/1.0/networks?access_token=${JWT}" | jq '.status_code')" -eq 200 ]

    # Ensure token not valid yet is not accepted
    JWT="$(./tls2jwt/tls2jwt "${INCUS_CONF}/jwt-client.crt" "${INCUS_CONF}/jwt-client.key" "2100-01-01T12:00:00Z" 60)"
    [ "$(curl -k -s -H "Authorization: Bearer ${JWT}" "https://${INCUS_ADDR}/1.0/networks" | jq '.error_code')" -eq 403 ]
    [ "$(curl -k -s "https://${INCUS_ADDR}/1.0/networks?access_token=${JWT}" | jq '.error_code')" -eq 403 ]

    # Ensure token no longer valid is not accepted
    JWT="$(./tls2jwt/tls2jwt "${INCUS_CONF}/jwt-client.crt" "${INCUS_CONF}/jwt-client.key" now -60)"
    [ "$(curl -k -s -H "Authorization: Bearer ${JWT}" "https://${INCUS_ADDR}/1.0/networks" | jq '.error_code')" -eq 403 ]
    [ "$(curl -k -s "https://${INCUS_ADDR}/1.0/networks?access_token=${JWT}" | jq '.error_code')" -eq 403 ]

    # Ensure the login endpoint rejects invalid tokens without setting a cookie
    rm -f "${TEST_DIR}/jwt-cookies"
    [ "$(curl -k -s -X POST -H "Authorization: Bearer 1234567" -c "${TEST_DIR}/jwt-cookies" "https://${INCUS_ADDR}/tls/login" | jq '.error_code')" -eq 403 ]
    ! grep -q "tls_jwt" "${TEST_DIR}/jwt-cookies" || false
    [ "$(curl -k -s -X POST -c "${TEST_DIR}/jwt-cookies" "https://${INCUS_ADDR}/tls/login" | jq '.error_code')" -eq 400 ]
    ! grep -q "tls_jwt" "${TEST_DIR}/jwt-cookies" || false

    # Ensure a valid token gets stored in a cookie which then authenticates requests
    JWT="$(./tls2jwt/tls2jwt "${INCUS_CONF}/jwt-client.crt" "${INCUS_CONF}/jwt-client.key" now 60)"
    [ "$(curl -k -s -X POST -H "Authorization: Bearer ${JWT}" -c "${TEST_DIR}/jwt-cookies" "https://${INCUS_ADDR}/tls/login" | jq '.status_code')" -eq 200 ]
    grep -q "tls_jwt" "${TEST_DIR}/jwt-cookies"
    [ "$(curl -k -s -b "${TEST_DIR}/jwt-cookies" "https://${INCUS_ADDR}/1.0/networks" | jq '.status_code')" -eq 200 ]

    # Ensure logout clears the cookie
    [ "$(curl -k -s -b "${TEST_DIR}/jwt-cookies" -c "${TEST_DIR}/jwt-cookies" "https://${INCUS_ADDR}/tls/logout" | jq '.status_code')" -eq 200 ]
    [ "$(curl -k -s -b "${TEST_DIR}/jwt-cookies" "https://${INCUS_ADDR}/1.0/networks" | jq '.error_code')" -eq 403 ]

    # Cleanup
    incus config trust remove "${FINGERPRINT}"
}
