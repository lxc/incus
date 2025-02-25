#!/bin/bash

set -e

# Logging functions
info() { echo -e "\e[34m[INFO] $1\e[0m"; }
success() { echo -e "\e[32m[SUCCESS] $1\e[0m"; }
error_msg() { echo -e "\e[31m[ERROR] $1\e[0m"; exit 1; }

# Ensure the container network is OVN-based
PARENT_NETWORK="incusbr0"
OVN_NETWORK="ovntest"

info "Ensuring OVN network is set up..."
incus network set "$PARENT_NETWORK" ipv4.dhcp.ranges="10.158.174.100-10.158.174.110" ipv4.ovn.ranges="10.158.174.111-10.158.174.120"
incus network create "$OVN_NETWORK" --type=ovn network="$PARENT_NETWORK" || true
# Get ovn ipv4 external address and setup routing:
ovnnet=$(incus network show ovntest | grep 'ipv4.address' | head -n 1 | cut -d ':' -f2)
ovnip=$(incus network show ovntest | grep 'network.ipv4.address' | head -n 1 | cut -d ':' -f2)
# Add route to ovn network
ip r a $(echo $ovnnet | awk -F. '{print $1"."$2"."$3".0/24"}') via $ovnip
# Allow icmp to go through ovn network
lsin=$(ovn-nbctl ls-list | awk '/-ls-int/ {gsub(/[()]/, "", $2); print $2}')
ovn-nbctl acl-add $lsin to-lport 200 "(icmp4 || icmp6)" allow
# Start a test container
info "Launching test container..."
incus init images:debian/12 testct
incus config device override testct eth0 network=ovntest
incus start testct
sleep 3

# Get container IPs
get_container_ip() { incus list testct --format json | jq -r '.[0].state.network.eth0.addresses[] | select(.family=="inet").address'; }
get_container_ip6() { incus list testct --format json | jq -r '.[0].state.network.eth0.addresses[] | select(.family=="inet6").address'; }

### **TEST 12: Block ICMPv4 using address-sets**
function test_block_ping_with_address_set() {
    info "Test 12: ACL block ICMPv4 for container"
    local ip=$(get_container_ip)
    info "Container IPv4: $ip"

    if ping -c2 "$ip" > /dev/null; then
        success "Ping to container succeeded."
    else
        error_msg "Ping to container failed."
    fi

    incus network address-set create testAS
    incus network address-set add-addr testAS "$ip"
    incus network acl create blockping
    incus network acl rule add blockping ingress action=drop protocol=icmp4 destination="\$testAS"
    incus network set "$OVN_NETWORK" security.acls="blockping"

    sleep 2
    echo "Get your insights"
    read
    if ping -c2 "$ip" > /dev/null; then
        error_msg "Ping succeeded despite ACL block."
    else
        success "Ping correctly blocked by ACL."
    fi

    incus network set "$OVN_NETWORK" security.acls=""
    incus network acl delete blockping
    incus network address-set delete testAS
}

### **TEST 13: Block ICMPv6 using address-sets**
function test_block_pingv6_with_address_set() {
    info "Test 13: ACL block ICMPv6 for container"
    local ip=$(get_container_ip6)
    info "Container IPv6: $ip"

    if ping6 -c2 "$ip" > /dev/null; then
        success "Ping to container succeeded."
    else
        error_msg "Ping to container failed."
    fi

    incus network address-set create testAS
    incus network address-set add-addr testAS "$ip"
    incus network acl create blockping
    incus network acl rule add blockping ingress action=drop protocol=icmp6 destination="\$testAS"
    incus network set "$OVN_NETWORK" security.acls="blockping"

    sleep 2
    if ping6 -c2 "$ip" > /dev/null; then
        error_msg "Ping succeeded despite ACL block."
    else
        success "Ping correctly blocked by ACL."
    fi

    incus network set "$OVN_NETWORK" security.acls=""
    incus network acl delete blockping
    incus network address-set delete testAS
}

### **TEST 14: Mixed ACL Subject**
function test_inner_acl_mixed_subject() {
    info "Test 14: ACL with mixed subject (literal IP and address set)"
    local ip=$(get_container_ip)

    incus network address-set create testAS
    incus network address-set add-addr testAS "$ip"
    incus launch images:debian/12 testct2
    sleep 3
    local ip2=$(get_container_ip testct2)

    incus network acl create mixedACL
    incus network acl rule add mixedACL ingress action=drop protocol=icmp4 destination="$ip2,\$testAS"
    incus network set "$OVN_NETWORK" security.acls="mixedACL"

    sleep 2
    if ping -c2 "$ip" > /dev/null || ping -c2 "$ip2" > /dev/null; then
        error_msg "Ping succeeded despite ACL block."
    else
        success "Ping correctly blocked by ACL."
    fi

    incus network set "$OVN_NETWORK" security.acls=""
    incus network acl delete mixedACL
    incus network address-set delete testAS
    incus delete testct2 --force
}

### **TEST 15: CIDR Address Set**
function test_inner_update_with_cidr() {
    info "Test 15: Update address set with CIDR"
    local ip=$(get_container_ip)
    local subnet=$(echo "$ip" | awk -F. '{print $1"."$2"."$3".0/24"}')

    incus network address-set create testAS
    incus network address-set add-addr testAS "$subnet"
    incus network acl create cidrACL
    incus network acl rule add cidrACL ingress action=drop protocol=icmp4 destination="\$testAS"
    incus network set "$OVN_NETWORK" security.acls="cidrACL"

    sleep 2
    if ping -c2 "$ip" > /dev/null; then
        error_msg "Ping succeeded despite CIDR ACL block."
    else
        success "Ping correctly blocked by ACL."
    fi

    incus network set "$OVN_NETWORK" security.acls=""
    incus network acl delete cidrACL
    incus network address-set delete testAS
}

### **TEST 16: IPv4/TCP Block**
function test_inner_block_tcp() {
    local ip=$(get_container_ip)
    incus exec testct -- apt update && apt install -y netcat-openbsd

    incus network address-set create testAS
    echo "Container ip: $ip"
    incus network address-set add-addr testAS "$ip"
    incus network acl create blocktcp7896
    incus network acl rule add blocktcp7896 ingress action=drop protocol=tcp destination_port="7896" destination="\$testAS"
    incus network set "$OVN_NETWORK" security.acls="blocktcp7896"

    info "Run: incus exec testct -- nohup nc -l -p 7896 &"
    read

    if nc -z -w 5 "$ip" 7896; then
        error_msg "TCP connection succeeded despite ACL block."
    else
        success "TCP connection correctly blocked by ACL."
    fi

    incus network set "$OVN_NETWORK" security.acls=""
    incus network acl delete blocktcp7896
    incus network address-set delete testAS
}

### **TEST 17: TCP Block with Mixed Address Sets**
function test_inner_block_tcp_mixed_set() {
    local ip=$(get_container_ip)
    local ip6=$(get_container_ip6)
    incus exec testct -- apt update && apt install -y netcat-openbsd

    incus network address-set create testAS
    incus network address-set add-addr testAS "$ip"
    incus network address-set add-addr testAS "$ip6"
    incus network acl create blocktcp7896
    incus network acl rule add blocktcp7896 ingress action=drop protocol=tcp destination_port="7896" destination="\$testAS"
    incus network set "$OVN_NETWORK" security.acls="blocktcp7896"

    info "Run: incus exec testct -- nohup nc -l -p 7896 &"
    read

    if nc -z -w 5 "$ip" 7896 || nc -6 -z -w 5 "$ip6" 7896; then
        error_msg "TCP connection succeeded despite ACL block."
    else
        success "TCP connection correctly blocked by ACL."
    fi

    incus network set "$OVN_NETWORK" security.acls=""
    incus network acl delete blocktcp7896
    incus network address-set delete testAS
}

# Run all tests
test_block_ping_with_address_set
#test_block_pingv6_with_address_set NOK
test_inner_acl_mixed_subject
test_inner_update_with_cidr
#test_inner_block_tcp
#test_inner_block_tcp_mixed_set

incus delete --force testct
incus network rm ovntest