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
            echo "yes" | ./easyrsa build-client-full incus-client nopass
            echo "yes" | ./easyrsa build-client-full incus-client-revoked nopass
            echo "yes" | ./easyrsa --subject-alt-name="IP:127.0.0.1" build-server-full incus-server-a nopass
            echo "yes" | ./easyrsa --subject-alt-name="IP:127.0.0.1" build-server-full incus-server-b nopass
            mkdir keys
            cp pki/private/* keys/
            cp pki/issued/* keys/
            cp pki/ca.crt keys/
            echo "yes" | ./easyrsa revoke incus-client-revoked
            ./easyrsa gen-crl
            cp pki/crl.pem keys/
        fi
    )

    # Setup the daemon in normal mode
    INCUS5_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    chmod +x "${INCUS5_DIR}"
    spawn_incus "${INCUS5_DIR}" true
    INCUS5_ADDR=$(cat "${INCUS5_DIR}/incus.addr")

    # Generate, trust and test a client certificate
    openssl req -x509 -newkey rsa:4096 -sha384 -keyout "${INCUS_CONF}/simple-client.key" -nodes -out "${INCUS_CONF}/simple-client.crt" -days 1 -subj "/CN=test.local"
    INCUS_DIR="${INCUS5_DIR}" incus config trust add-certificate "${INCUS_CONF}/simple-client.crt"
    INCUS_DIR="${INCUS5_DIR}" incus config set user.test foo
    curl -k -s --cert "${INCUS_CONF}/simple-client.crt" --key "${INCUS_CONF}/simple-client.key" "https://${INCUS5_ADDR}/1.0" | grep -q "user.test.*foo" || false

    # Restart the daemon in PKI mode
    shutdown_incus "${INCUS5_DIR}"
    cp "${TEST_DIR}/pki/keys/ca.crt" "${INCUS5_DIR}/server.ca"
    cp "${TEST_DIR}/pki/keys/crl.pem" "${INCUS5_DIR}/ca.crl"
    respawn_incus "${INCUS5_DIR}" true

    # Setup the client.
    INC5_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    cp "${TEST_DIR}/pki/keys/incus-client.crt" "${INC5_DIR}/client.crt"
    cp "${TEST_DIR}/pki/keys/incus-client.key" "${INC5_DIR}/client.key"
    cp "${TEST_DIR}/pki/keys/ca.crt" "${INC5_DIR}/client.ca"

    # Re-test the regular client certificate
    curl -k -s --cert "${INCUS_CONF}/simple-client.crt" --key "${INCUS_CONF}/simple-client.key" "https://${INCUS5_ADDR}/1.0" incus/1.0 | grep -q "user.test.*foo" && false
    fingerprint="$(INCUS_DIR="${INCUS5_DIR}" incus config trust list -cf -fcsv)"
    INCUS_DIR="${INCUS5_DIR}" incus config trust remove "${fingerprint}"

    # Confirm that a valid client certificate works.
    (
        set -e
        # shellcheck disable=SC2030
        export INCUS_CONF="${INC5_DIR}"

        # Try adding remote using an incorrect token.
        # This should fail, as if the certificate is unknown and token is wrong then no access should be allowed.
        ! incus_remote remote add pki-incus "${INCUS5_ADDR}" --accept-certificate --token=bar || false

        # Add remote using the correct token.
        # This should work because the client certificate is signed by the CA.
        token="$(INCUS_DIR=${INCUS5_DIR} incus config trust add foo -q)"
        incus_remote remote add pki-incus "${INCUS5_ADDR}" --accept-certificate --token "${token}"
        incus_remote config trust list pki-incus: -cc -fcsv | grep incus-client
        fingerprint="$(incus_remote config trust list pki-incus: -cf -fcsv)"
        incus_remote config trust remove pki-incus:"${fingerprint}"
        incus_remote remote remove pki-incus

        # Add remote using a CA-signed client certificate, and not providing a token.
        # This should succeed and tests that the CA trust is working, as adding the client certificate to the trust
        # store without a token would normally fail.
        INCUS_DIR=${INCUS5_DIR} incus config set core.trust_ca_certificates true
        incus_remote remote add pki-incus "${INCUS5_ADDR}" --accept-certificate
        ! incus_remote config trust list pki-incus: -cc -fcsv | grep incus-client || false
        incus_remote remote remove pki-incus

        # Add remote using a CA-signed client certificate, and providing an incorrect token.
        # This should succeed as is the same as the test above but with an incorrect token rather than no token.
        incus_remote remote add pki-incus "${INCUS5_ADDR}" --accept-certificate --token=bar
        ! incus_remote config trust list pki-incus: -cc -fcsv | grep incus-client || false
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

    # The same applies to a certificate sent in the request body, only a CA-signed one gets through.
    gen_cert_and_key "${TEST_DIR}/pki-body.key" "${TEST_DIR}/pki-body.crt" "incus.local"
    cert="$(sed ':a;N;$!ba;s/\n/\\n/g' "${TEST_DIR}/pki-body.crt")"
    [ "$(curl -k -s -X POST -d "{\"trust_token\":\"${token}\",\"certificate\":\"${cert}\"}" "https://${INCUS5_ADDR}/1.0/certificates" | jq '.error_code')" -eq 403 ]
    token="$(INCUS_DIR=${INCUS5_DIR} incus config trust add foo -q)"
    cert="$(sed ':a;N;$!ba;s/\n/\\n/g' "${TEST_DIR}/pki/keys/incus-client.crt")"
    [ "$(curl -k -s -X POST -d "{\"trust_token\":\"${token}\",\"certificate\":\"${cert}\"}" "https://${INCUS5_ADDR}/1.0/certificates" | jq '.status_code')" -eq 200 ]
    INCUS_DIR="${INCUS5_DIR}" incus config trust show "$(cert_fingerprint "${TEST_DIR}/pki/keys/incus-client.crt")" | grep -xF "name: foo"

    # Copies between two servers with CA-issued certificates, validated by the client through the CA alone.
    if [ -e "${TEST_DIR}/pki/keys/incus-server-a.crt" ]; then
        shutdown_incus "${INCUS5_DIR}"
        cp "${TEST_DIR}/pki/keys/incus-server-a.crt" "${INCUS5_DIR}/server.crt"
        cp "${TEST_DIR}/pki/keys/incus-server-a.key" "${INCUS5_DIR}/server.key"
        respawn_incus "${INCUS5_DIR}" true

        INCUS6_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
        chmod +x "${INCUS6_DIR}"
        cp "${TEST_DIR}/pki/keys/incus-server-b.crt" "${INCUS6_DIR}/server.crt"
        cp "${TEST_DIR}/pki/keys/incus-server-b.key" "${INCUS6_DIR}/server.key"
        cp "${TEST_DIR}/pki/keys/ca.crt" "${INCUS6_DIR}/server.ca"
        spawn_incus "${INCUS6_DIR}" true
        INCUS6_ADDR=$(cat "${INCUS6_DIR}/incus.addr")
        INCUS_DIR="${INCUS6_DIR}" incus config set core.trust_ca_certificates true

        cp "${TEST_DIR}/pki/keys/incus-client.crt" "${INC5_DIR}/client.crt"
        cp "${TEST_DIR}/pki/keys/incus-client.key" "${INC5_DIR}/client.key"
        (
            set -e
            # shellcheck disable=SC2031
            export INCUS_CONF="${INC5_DIR}"

            incus_remote remote add pki-a "${INCUS5_ADDR}"
            incus_remote remote add pki-b "${INCUS6_ADDR}"
            [ ! -e "${INC5_DIR}/servercerts/pki-a.crt" ]
            [ ! -e "${INC5_DIR}/servercerts/pki-b.crt" ]

            INCUS_DIR="${INCUS5_DIR}" ensure_import_testimage
            incus_remote init pki-a:testimage pki-a:c1
            incus_remote copy pki-a:c1 pki-b:c1
            incus_remote info pki-b:c1 | grep -q "Status: STOPPED"

            incus_remote image copy pki-a:testimage pki-b: --alias testimage
            incus_remote image info pki-b:testimage | grep -q "Fingerprint"

            incus_remote storage volume create pki-a:"incustest-$(basename "${INCUS5_DIR}")" vol1
            incus_remote storage volume copy pki-a:"incustest-$(basename "${INCUS5_DIR}")/vol1" pki-b:"incustest-$(basename "${INCUS6_DIR}")/vol1"
            incus_remote storage volume delete pki-b:"incustest-$(basename "${INCUS6_DIR}")" vol1
            incus_remote storage volume delete pki-a:"incustest-$(basename "${INCUS5_DIR}")" vol1

            incus_remote delete pki-a:c1 pki-b:c1
            incus_remote image delete pki-a:testimage pki-b:testimage
            incus_remote remote remove pki-a
            incus_remote remote remove pki-b
        )

        kill_incus "${INCUS6_DIR}"
    fi

    kill_incus "${INCUS5_DIR}"
}
