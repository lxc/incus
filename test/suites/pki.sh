test_pki() {
  if [ ! -d "/usr/share/easy-rsa/" ]; then
    echo "==> SKIP: The pki test requires easy-rsa to be installed"
    return
  fi

  # Setup the PKI.
  cp -R /usr/share/easy-rsa "${TEST_DIR}/pki"
  (
    set -e
    cd "${TEST_DIR}/pki"
    export EASYRSA_KEY_SIZE=4096

    # shellcheck disable=SC1091
    if [ -e pkitool ]; then
        . ./vars
        ./clean-all
        ./pkitool --initca
        ./pkitool incus-client
        ./pkitool incus-client-revoked
        # This will revoke the certificate but fail in the end as it tries to then verify the revoked certificate.
        ./revoke-full incus-client-revoked || true
    else
        ./easyrsa init-pki
        echo "incus" | ./easyrsa build-ca nopass
        ./easyrsa gen-crl
        ./easyrsa build-client-full incus-client nopass
        ./easyrsa build-client-full incus-client-revoked nopass
        mkdir keys
        cp pki/private/* keys/
        cp pki/issued/* keys/
        cp pki/ca.crt keys/
        echo "yes" | ./easyrsa revoke incus-client-revoked
        ./easyrsa gen-crl
        cp pki/crl.pem keys/
    fi
  )

  # Setup the daemon.
  INCUS5_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS5_DIR}"
  cp "${TEST_DIR}/pki/keys/ca.crt" "${INCUS5_DIR}/server.ca"
  cp "${TEST_DIR}/pki/keys/crl.pem" "${INCUS5_DIR}/ca.crl"
  spawn_incus "${INCUS5_DIR}" true
  INCUS5_ADDR=$(cat "${INCUS5_DIR}/incus.addr")

  # Setup the client.
  INC5_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  cp "${TEST_DIR}/pki/keys/incus-client.crt" "${INC5_DIR}/client.crt"
  cp "${TEST_DIR}/pki/keys/incus-client.key" "${INC5_DIR}/client.key"
  cp "${TEST_DIR}/pki/keys/ca.crt" "${INC5_DIR}/client.ca"

  # Confirm that a valid client certificate works.
  (
    set -e
    export INCUS_CONF="${INC5_DIR}"

    # Try adding remote using an incorrect token.
    # This should fail, as if the certificate is unknown and token is wrong then no access should be allowed.
    ! incus_remote remote add pki-incus "${INCUS5_ADDR}" --accept-certificate --token=bar || false

    # Add remote using the correct token.
    # This should work because the client certificate is signed by the CA.
    token="$(INCUS_DIR=${INCUS5_DIR} incus config trust add foo -q)"
    incus_remote remote add pki-incus "${INCUS5_ADDR}" --accept-certificate --token "${token}"
    incus_remote config trust ls pki-incus: | grep incus-client
    fingerprint="$(incus_remote config trust ls pki-incus: --format csv | cut -d, -f4)"
    incus_remote config trust remove pki-incus:"${fingerprint}"
    incus_remote remote remove pki-incus

    # Add remote using a CA-signed client certificate, and not providing a token.
    # This should succeed and tests that the CA trust is working, as adding the client certificate to the trust
    # store without a token would normally fail.
    INCUS_DIR=${INCUS5_DIR} incus config set core.trust_ca_certificates true
    incus_remote remote add pki-incus "${INCUS5_ADDR}" --accept-certificate
    ! incus_remote config trust ls pki-incus: | grep incus-client || false
    incus_remote remote remove pki-incus

    # Add remote using a CA-signed client certificate, and providing an incorrect token.
    # This should succeed as is the same as the test above but with an incorrect token rather than no token.
    incus_remote remote add pki-incus "${INCUS5_ADDR}" --accept-certificate --token=bar
    ! incus_remote config trust ls pki-incus: | grep incus-client || false
    incus_remote remote remove pki-incus

    # Replace the client certificate with a revoked certificate in the CRL.
    cp "${TEST_DIR}/pki/keys/incus-client-revoked.crt" "${INC5_DIR}/client.crt"
    cp "${TEST_DIR}/pki/keys/incus-client-revoked.key" "${INC5_DIR}/client.key"

    # Try adding a remote using a revoked client certificate, and the correct token.
    # This should fail, as although revoked certificates can be added to the trust store, they will not be usable.
    token="$(INCUS_DIR=${INCUS5_DIR} incus config trust add foo -q)"
    ! incus_remote remote add pki-incus "${INCUS5_ADDR}" --accept-certificate --token "${token}" || false

    # Try adding a remote using a revoked client certificate, and an incorrect token.
    # This should fail, as if the certificate is revoked and token is wrong then no access should be allowed.
    ! incus_remote remote add pki-incus "${INCUS5_ADDR}" --accept-certificate --token=incorrect || false
  )

  # Confirm that a normal, non-PKI certificate doesn't.
  # As INCUS_CONF is not set to INC5_DIR where the CA signed client certs are, this will cause the incus command to
  # generate a new certificate that isn't trusted by the CA certificate and thus will not be allowed, even with a
  # correct token. This is because the Incus TLS listener in CA mode will not consider a client cert that
  # is not signed by the CA as valid.
  token="$(INCUS_DIR=${INCUS5_DIR} incus config trust add foo -q)"
  ! incus_remote remote add pki-incus "${INCUS5_ADDR}" --accept-certificate --token "${token}" || false

  kill_incus "${INCUS5_DIR}"
}
