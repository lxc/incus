test_authorization_scriptlet() {
  incus config set core.https_address "${INCUS_ADDR}"
  ensure_has_localhost_remote "${INCUS_ADDR}"
  ensure_import_testimage

  # Check only valid scriptlets are accepted.
  ! incus config set authorization.scriptlet=foo || false

  # Prevent user1 from doing anything, except viewing the server
  cat << EOF | incus config set authorization.scriptlet=-
def authorize(details, object, entitlement):
  if details.Username == 'user1':
    return object == 'server:incus' and entitlement == 'can_view'
  return True
EOF

  # Run OIDC server.
  spawn_oidc
  set_oidc user1

  incus config set "oidc.issuer=http://127.0.0.1:$(cat "${TEST_DIR}/oidc.port")/"
  incus config set "oidc.client.id=device"

  BROWSER=curl incus remote add --accept-certificate oidc-authorization-scriptlet "${INCUS_ADDR}" --auth-type oidc
  [ "$(incus info oidc-authorization-scriptlet: | grep ^auth_user_name | sed "s/.*: //g")" = "user1" ]

  # user1 can’t see anything yet
  [ "$(incus project list oidc-authorization-scriptlet: -f csv | wc -l)" = 0 ]
  [ "$(incus list oidc-authorization-scriptlet: -f csv | wc -l)" = 0 ]

  # Let’s fix that
  cat << EOF | incus config set authorization.scriptlet=-
def authorize(details, object, entitlement):
  if details.Username == 'user1':
    return object in ['server:incus', 'project:default'] and entitlement == 'can_view'
  return True
EOF

  # user1 can see the project but not create an instance
  [ "$(incus project list oidc-authorization-scriptlet: -f csv | wc -l)" = 1 ]
  [ "$(incus list oidc-authorization-scriptlet: -f csv | wc -l)" = 0 ]
  ! incus init oidc-authorization-scriptlet:testimage oidc-authorization-scriptlet:c1

  # Let’s fix that
  cat << EOF | incus config set authorization.scriptlet=-
def authorize(details, object, entitlement):
  if details.Username == 'user1':
    if (object in ['server:incus', 'image_alias:default/testimage'] or object.startswith('image:default/') or object.startswith('instance:default/')):
      return entitlement == 'can_view'
    elif object == 'project:default':
      return entitlement in ['can_view', 'can_create_instances', 'can_view_events']
    return False
  return True
EOF

  # user1 can create an instance, but not interact with it
  incus init oidc-authorization-scriptlet:testimage oidc-authorization-scriptlet:c1
  [ "$(incus list oidc-authorization-scriptlet: -f csv | wc -l)" = 1 ]
  ! incus exec oidc-authorization-scriptlet:c1 -- ls -al

  # Let’s fix that
  cat << EOF | incus config set authorization.scriptlet=-
def authorize(details, object, entitlement):
  if details.Username == 'user1':
    if object == 'instance:default/c1':
      return entitlement in ['can_view', 'can_update_state', 'can_exec']
    elif (object in ['server:incus', 'image_alias:default/testimage'] or object.startswith('image:default/') or object.startswith('instance:default/')):
      return entitlement == 'can_view'
    elif object == 'project:default':
      return entitlement in ['can_view', 'can_create_instances', 'can_view_events']
    return False
  return True
EOF

  # user1 can execute commands on c1 but cannot do anything outside of the default project
  incus start oidc-authorization-scriptlet:c1
  incus exec oidc-authorization-scriptlet:c1 -- ls -al
  ! incus project create oidc-authorization-scriptlet:p1

  # Let’s fix that
  cat << EOF | incus config set authorization.scriptlet=-
def authorize(details, object, entitlement):
  if details.Username == 'user1':
    if object == 'instance:default/c1':
      return entitlement in ['can_view', 'can_update_state', 'can_exec']
    elif object == 'server:incus':
      return entitlement in ['can_view', 'can_create_projects']
    elif (object == 'image_alias:default/testimage' or object.startswith('image:default/') or object.startswith('instance:default/')):
      return entitlement == 'can_view'
    elif object in ['project:default', 'project:p1']:
      return entitlement in ['can_view', 'can_create_instances', 'can_view_events']
    return False
  return True
EOF

  incus project create oidc-authorization-scriptlet:p1
  [ "$(incus project list oidc-authorization-scriptlet: -f csv | wc -l)" = 2 ]

  # Let’s now test the two optional scriptlet functions
  cat << EOF | incus config set authorization.scriptlet=-
def authorize(details, object, entitlement):
  return True

def get_project_access(project_name):
  if project_name == 'default':
    return ['foo', 'bar']
  return ['foo']

def get_instance_access(project_name, instance_name):
  if project_name == 'default' and instance_name == 'c1':
    return ['foo', 'bar']
  return ['foo']
EOF

  [ "$(incus project info default --show-access | wc -l)" = 6 ]
  [ "$(incus project info p1 --show-access | wc -l)" = 3 ]
  [ "$(incus info c1 --show-access | wc -l)" = 6 ]
  incus init testimage c2
  [ "$(incus info c2 --show-access | wc -l)" = 3 ]

  # Cleanup.
  incus delete c1 --force
  incus delete c2 --force
  incus project delete p1

  # Unset config keys.
  kill_oidc
  incus config unset oidc.issuer
  incus config unset oidc.client.id
  incus config unset authorization.scriptlet
  incus remote remove oidc-authorization-scriptlet
}
