test_template() {
  # shellcheck disable=2039,3043
  local incus_backend
  incus_backend=$(storage_backend "$INCUS_DIR")

  # Import a template which only triggers on create
  deps/import-busybox --alias template-test --template create
  lxc init template-test template

  # Confirm that template application is delayed to first start
  ! lxc file pull template/template - || false

  # Validate that the template is applied
  lxc start template
  lxc file pull template/template - | grep "^name: template$"

  if [ "$incus_backend" = "lvm" ] || [ "$incus_backend" = "ceph" ]; then
    lxc stop template --force
  fi

  # Confirm it's not applied on copies
  lxc copy template template1
  lxc file pull template1/template - | grep "^name: template$"

  # Cleanup
  lxc image delete template-test
  lxc delete template template1 --force


  # Import a template which only triggers on copy
  deps/import-busybox --alias template-test --template copy
  lxc launch template-test template

  # Confirm that the template doesn't trigger on create
  ! lxc file pull template/template - || false
  if [ "$incus_backend" = "lvm" ] || [ "$incus_backend" = "ceph" ]; then
    lxc stop template --force
  fi

  # Copy the container
  lxc copy template template1

  # Confirm that template application is delayed to first start
  ! lxc file pull template1/template - || false

  # Validate that the template is applied
  lxc start template1
  lxc file pull template1/template - | grep "^name: template1$"

  # Cleanup
  lxc image delete template-test
  lxc delete template template1 --force


  # Import a template which only triggers on start
  deps/import-busybox --alias template-test --template start
  lxc launch template-test template

  # Validate that the template is applied
  lxc file pull template/template - | grep "^name: template$"
  lxc file pull template/template - | grep "^user.foo: _unset_$"

  # Confirm it's re-run at every start
  lxc config set template user.foo bar
  lxc restart template --force
  lxc file pull template/template - | grep "^user.foo: bar$"

  # Cleanup
  lxc image delete template-test
  lxc delete template --force


  # Import a template which triggers on both create and copy
  deps/import-busybox --alias template-test --template create,copy
  lxc init template-test template

  # Confirm that template application is delayed to first start
  ! lxc file pull template/template - || false

  # Validate that the template is applied
  lxc start template
  lxc file pull template/template - | grep "^name: template$"

  # Confirm it's also applied on copies
  lxc copy template template1
  lxc start template1
  lxc file pull template1/template - | grep "^name: template1$"
  lxc file pull template1/template - | grep "^user.foo: _unset_$"

  # But doesn't change on restart
  lxc config set template1 user.foo bar
  lxc restart template1 --force
  lxc file pull template1/template - | grep "^user.foo: _unset_$"

  # Cleanup
  lxc image delete template-test
  lxc delete template template1 --force
}
