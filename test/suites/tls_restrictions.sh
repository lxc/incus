test_tls_restrictions() {
    ensure_import_testimage
    ensure_has_localhost_remote "${INCUS_ADDR}"

    gen_cert_and_key "${INCUS_CONF}/metrics.key" "${INCUS_CONF}/metrics.crt" "metrics.local"

    # Ensure type=metrics certificates cannot access anything besides /1.0/metrics.
    curl -k -s --cert "${INCUS_CONF}/metrics.crt" --key "${INCUS_CONF}/metrics.key" "https://${INCUS_ADDR}/1.0/metrics" | grep -F '"error_code":403'
    incus config trust add-certificate "${INCUS_CONF}/metrics.crt" --type=metrics
    curl -k -s --cert "${INCUS_CONF}/metrics.crt" --key "${INCUS_CONF}/metrics.key" "https://${INCUS_ADDR}/1.0/metrics" | grep -Fx '# EOF'

    curl -k -s --cert "${INCUS_CONF}/metrics.crt" --key "${INCUS_CONF}/metrics.key" "https://${INCUS_ADDR}/1.0/certificates" | grep -F '"error_code":403'

    # Cleanup type=metrics certificate.
    METRICS_FINGERPRINT="$(incus config trust list --format csv --columns cf | grep -F metrics.local | cut -d, -f2)"
    incus config trust remove "${METRICS_FINGERPRINT}"

    FINGERPRINT="$(incus config trust list --format csv --columns nf | grep -F foo | cut -d, -f2)"

    # Validate admin rights with no restrictions
    incus_remote project create localhost:blah

    # Validate normal view with no restrictions
    incus_remote project list localhost: | grep -q default
    incus_remote project list localhost: | grep -q blah

    # Apply restrictions
    incus config trust show "${FINGERPRINT}" | sed -e "s/restricted: false/restricted: true/" | incus config trust edit "${FINGERPRINT}"

    # Confirm no project visible when none listed
    [ "$(incus_remote project list localhost: --format csv | wc -l)" = 0 ]

    # Allow access to project blah
    incus config trust show "${FINGERPRINT}" | sed -e "s/projects: \[\]/projects: ['blah']/" -e "s/restricted: false/restricted: true/" | incus config trust edit "${FINGERPRINT}"

    # Validate restricted view
    ! incus_remote project list localhost: | grep -q default || false
    incus_remote project list localhost: | grep -q blah

    ! incus_remote project create localhost:blah1 || false

    # Confirm inability to change certificate type
    ! incus query "/1.0/certificates/${FINGERPRINT}" -X PATCH -d '{"type": "server"}' || false

    # Certificates issued by a trusted CA certificate are trusted as the CA's entry.
    openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:secp384r1 -sha384 -keyout "${INCUS_CONF}/ca.key" -nodes -out "${INCUS_CONF}/ca.crt" -days 1 -subj "/CN=ca.local"
    openssl req -newkey ec -pkeyopt ec_paramgen_curve:secp384r1 -nodes -keyout "${INCUS_CONF}/leaf.key" -out "${INCUS_CONF}/leaf.csr" -subj "/CN=leaf.local"
    openssl x509 -req -in "${INCUS_CONF}/leaf.csr" -CA "${INCUS_CONF}/ca.crt" -CAkey "${INCUS_CONF}/ca.key" -CAcreateserial -sha384 -days 1 -out "${INCUS_CONF}/leaf.crt"
    CA_FINGERPRINT="$(cert_fingerprint "${INCUS_CONF}/ca.crt")"

    curl -k -s --cert "${INCUS_CONF}/leaf.crt" --key "${INCUS_CONF}/leaf.key" "https://${INCUS_ADDR}/1.0/projects" | grep -F '"error_code":403'
    incus config trust add-certificate "${INCUS_CONF}/ca.crt" --name ca.local --restricted --projects blah
    curl -k -s --cert "${INCUS_CONF}/leaf.crt" --key "${INCUS_CONF}/leaf.key" "https://${INCUS_ADDR}/1.0" | grep -F "\"auth_user_name\":\"${CA_FINGERPRINT}\""
    curl -k -s --cert "${INCUS_CONF}/leaf.crt" --key "${INCUS_CONF}/leaf.key" "https://${INCUS_ADDR}/1.0/projects" | grep -F '"/1.0/projects/blah"'
    ! curl -k -s --cert "${INCUS_CONF}/leaf.crt" --key "${INCUS_CONF}/leaf.key" "https://${INCUS_ADDR}/1.0/projects" | grep -F '"/1.0/projects/default"' || false

    # The issued certificate cannot alter or renew the CA entry.
    curl -k -s --cert "${INCUS_CONF}/leaf.crt" --key "${INCUS_CONF}/leaf.key" -X PATCH -d '{"restricted": false}' "https://${INCUS_ADDR}/1.0/certificates/${CA_FINGERPRINT}" | grep -F '"error_code":403'
    curl -k -s --cert "${INCUS_CONF}/leaf.crt" --key "${INCUS_CONF}/leaf.key" -X PATCH -d "{\"certificate\":\"$(sed ':a;N;$!ba;s/\n/\\n/g' "${INCUS_CONF}/leaf.crt")\"}" "https://${INCUS_ADDR}/1.0/certificates/${CA_FINGERPRINT}" | grep -F '"error_code":403'
    curl -k -s --cert "${INCUS_CONF}/leaf.crt" --key "${INCUS_CONF}/leaf.key" -X DELETE "https://${INCUS_ADDR}/1.0/certificates/${CA_FINGERPRINT}" | grep -F '"error_code":403'

    incus config trust remove "${CA_FINGERPRINT}"
    curl -k -s --cert "${INCUS_CONF}/leaf.crt" --key "${INCUS_CONF}/leaf.key" "https://${INCUS_ADDR}/1.0/projects" | grep -F '"error_code":403'
    rm -f "${INCUS_CONF}/ca.key" "${INCUS_CONF}/ca.crt" "${INCUS_CONF}/ca.srl" "${INCUS_CONF}/leaf.key" "${INCUS_CONF}/leaf.csr" "${INCUS_CONF}/leaf.crt"

    # Cleanup
    incus config trust show "${FINGERPRINT}" | sed -e "s/restricted: true/restricted: false/" | incus config trust edit "${FINGERPRINT}"
    incus project delete blah
}

test_certificate_edit() {
    ensure_import_testimage
    ensure_has_localhost_remote "${INCUS_ADDR}"

    # Generate a certificate
    gen_cert_and_key "${INCUS_CONF}/client.key.new" "${INCUS_CONF}/client.crt.new" "test.local"

    FINGERPRINT="$(incus config trust list --format csv --columns f)"

    # Try replacing the old certificate with a new one.
    # This should succeed as the user is listed as an admin.
    curl -k -s --cert "${INCUS_CONF}/client.crt" --key "${INCUS_CONF}/client.key" -X PATCH -d "{\"certificate\":\"$(sed ':a;N;$!ba;s/\n/\\n/g' "${INCUS_CONF}/client.crt.new")\"}" "https://${INCUS_ADDR}/1.0/certificates/${FINGERPRINT}"

    # Record new fingerprint
    FINGERPRINT="$(incus config trust list --format csv --columns f)"

    # Move new certificate and key to INCUS_CONF and back up old files.
    mv "${INCUS_CONF}/client.crt" "${INCUS_CONF}/client.crt.bak"
    mv "${INCUS_CONF}/client.key" "${INCUS_CONF}/client.key.bak"
    mv "${INCUS_CONF}/client.crt.new" "${INCUS_CONF}/client.crt"
    mv "${INCUS_CONF}/client.key.new" "${INCUS_CONF}/client.key"

    incus_remote project create localhost:blah

    # Apply restrictions
    incus config trust show "${FINGERPRINT}" | sed -e "s/restricted: false/restricted: true/" | incus config trust edit "${FINGERPRINT}"

    # Add created project to the list of restricted projects. This way, the user will be listed as
    # a normal user instead of an admin.
    incus config trust show "${FINGERPRINT}" | sed -e "s/projects: \[\]/projects: \[blah\]/" | incus config trust edit "${FINGERPRINT}"

    # Try replacing the new certificate with the old one.
    # This should succeed as well as the certificate may be changed.
    curl -k -s --cert "${INCUS_CONF}/client.crt" --key "${INCUS_CONF}/client.key" -X PATCH -d "{\"certificate\":\"$(sed ':a;N;$!ba;s/\n/\\n/g' "${INCUS_CONF}/client.crt.bak")\"}" "https://${INCUS_ADDR}/1.0/certificates/${FINGERPRINT}"

    # Move new certificate and key to INCUS_CONF.
    mv "${INCUS_CONF}/client.crt.bak" "${INCUS_CONF}/client.crt"
    mv "${INCUS_CONF}/client.key.bak" "${INCUS_CONF}/client.key"

    # Record new fingerprint
    FINGERPRINT="$(incus config trust list --format csv --columns f)"

    # Trying to change other fields should fail as a non-admin.
    ! incus_remote config trust show "${FINGERPRINT}" | sed -e "s/restricted: true/restricted: false/" | incus_remote config trust edit localhost:"${FINGERPRINT}" || false

    curl -k -s --cert "${INCUS_CONF}/client.crt" --key "${INCUS_CONF}/client.key" -X PATCH -d "{\"restricted\": false}" "https://${INCUS_ADDR}/1.0/certificates/${FINGERPRINT}" | grep -F '"error_code":403'

    ! incus_remote config trust show "${FINGERPRINT}" | sed -e "s/name:.*/name: bar/" | incus_remote config trust edit localhost:"${FINGERPRINT}" || false

    curl -k -s --cert "${INCUS_CONF}/client.crt" --key "${INCUS_CONF}/client.key" -X PATCH -d "{\"name\": \"bar\"}" "https://${INCUS_ADDR}/1.0/certificates/${FINGERPRINT}" | grep -F '"error_code":403'

    ! incus_remote config trust show "${FINGERPRINT}" | sed -e ':a;N;$!ba;s/projects:\n  - blah/projects: \[\]/' | incus_remote config trust edit localhost:"${FINGERPRINT}" || false

    curl -k -s --cert "${INCUS_CONF}/client.crt" --key "${INCUS_CONF}/client.key" -X PATCH -d "{\"projects\": []}" "https://${INCUS_ADDR}/1.0/certificates/${FINGERPRINT}" | grep -F '"error_code":403'

    # Cleanup
    incus config trust show "${FINGERPRINT}" | sed -e "s/restricted: true/restricted: false/" | incus config trust edit "${FINGERPRINT}"

    incus config trust show "${FINGERPRINT}" | sed -e ':a;N;$!ba;s/projects:\n  - blah/projects: \[\]/' | incus config trust edit "${FINGERPRINT}"

    incus project delete blah
}
