test_image_nil_profile_list() {
  # Launch container with default profile list and check its profiles
  ensure_import_testimage
  incus launch testimage c1
  incus list -f json c1 | jq -r '.[0].profiles | join(" ")' | grep -q "default" || false

  # Cleanup
  incus delete c1 -f
  incus image delete testimage
}

test_image_empty_profile_list() {
  # Set the profiles to be an empty list
  ensure_import_testimage
  incus image show testimage | sed "s/profiles.*/profiles: []/; s/- default//" | incus image edit testimage

  # Check that the profile list is correct
  incus image show testimage | grep -q 'profiles: \[\]' || false
  ! incus image show testimage | grep -q -- '- default' || false

  # Launch the container and check its profiles
  storage=$(incus storage list | grep "^| " | tail -n 1 | cut -d' ' -f2)
  incus launch testimage c1 -s "$storage"
  incus list -f json c1 | jq -r '.[0].profiles | join(" ")' | grep -q "^$" || false

  # Cleanup
  incus delete c1 -f
  incus image delete testimage
}

test_image_alternate_profile_list() {
  # Add three new profiles to the profile list
  ensure_import_testimage
  incus profile create p1
  incus profile create p2
  incus profile create p3
  incus image show testimage | sed "s/profiles.*/profiles: ['p1','p2','p3']/; s/- default//" | incus image edit testimage

  # Check that the profile list is correct
  incus image show testimage | grep -q -- '- p1' || false
  incus image show testimage | grep -q -- '- p2' || false
  incus image show testimage | grep -q -- '- p3' || false
  ! incus image show testimage | grep -q -- '- default' || false

  # Launch the container and check its profiles
  storage=$(incus storage list | grep "^| " | tail -n 1 | cut -d' ' -f2)
  incus profile device add p1 root disk path=/ pool="$storage"
  incus launch testimage c1
  incus list -f json c1 | jq -r '.[0].profiles | join(" ")' | grep -q "p1 p2 p3" || false

  # Cleanup
  incus delete c1 -f
  incus profile delete p1
  incus profile delete p2
  incus profile delete p3
  incus image delete testimage
}

test_profiles_project_default() {
  incus project switch default
  test_image_nil_profile_list
  test_image_empty_profile_list
  test_image_alternate_profile_list
}

test_profiles_project_images_profiles() {
  incus project create project1
  incus project switch project1
  storage=$(incus storage list | grep "^| " | tail -n 1 | cut -d' ' -f2)
  incus profile device add default root disk path=/ pool="$storage"

  test_image_nil_profile_list
  test_image_empty_profile_list
  test_image_alternate_profile_list

  incus project switch default
  incus project delete project1
}

# Run the tests with a project that only has the features.images enabled
test_profiles_project_images() {
  incus project create project1 -c features.profiles=false
  incus project switch project1

  test_image_nil_profile_list
  test_image_empty_profile_list
  test_image_alternate_profile_list

  incus project switch default
  incus project delete project1
}

test_profiles_project_profiles() {
  incus project create project1 -c features.images=false
  incus project switch project1
  storage=$(incus storage list | grep "^| " | tail -n 1 | cut -d' ' -f2)
  incus profile device add default root disk path=/ pool="$storage"

  test_image_nil_profile_list
  test_image_empty_profile_list
  test_image_alternate_profile_list

  incus project switch default
  incus project delete project1
}
