test_image_nil_profile_list() {
  # Launch container with default profile list and check its profiles
  ensure_import_testimage
  inc launch testimage c1
  inc list -f json c1 | jq -r '.[0].profiles | join(" ")' | grep -q "default" || false

  # Cleanup
  inc delete c1 -f
  inc image delete testimage
}

test_image_empty_profile_list() {
  # Set the profiles to be an empty list
  ensure_import_testimage
  inc image show testimage | sed "s/profiles.*/profiles: []/; s/- default//" | inc image edit testimage

  # Check that the profile list is correct
  inc image show testimage | grep -q 'profiles: \[\]' || false
  ! inc image show testimage | grep -q -- '- default' || false

  # Launch the container and check its profiles
  storage=$(inc storage list | grep "^| " | tail -n 1 | cut -d' ' -f2)
  inc launch testimage c1 -s "$storage"
  inc list -f json c1 | jq -r '.[0].profiles | join(" ")' | grep -q "^$" || false

  # Cleanup
  inc delete c1 -f
  inc image delete testimage
}

test_image_alternate_profile_list() {
  # Add three new profiles to the profile list
  ensure_import_testimage
  inc profile create p1
  inc profile create p2
  inc profile create p3
  inc image show testimage | sed "s/profiles.*/profiles: ['p1','p2','p3']/; s/- default//" | inc image edit testimage

  # Check that the profile list is correct
  inc image show testimage | grep -q -- '- p1' || false
  inc image show testimage | grep -q -- '- p2' || false
  inc image show testimage | grep -q -- '- p3' || false
  ! inc image show testimage | grep -q -- '- default' || false

  # Launch the container and check its profiles
  storage=$(inc storage list | grep "^| " | tail -n 1 | cut -d' ' -f2)
  inc profile device add p1 root disk path=/ pool="$storage"
  inc launch testimage c1
  inc list -f json c1 | jq -r '.[0].profiles | join(" ")' | grep -q "p1 p2 p3" || false

  # Cleanup
  inc delete c1 -f
  inc profile delete p1
  inc profile delete p2
  inc profile delete p3
  inc image delete testimage
}

test_profiles_project_default() {
  inc project switch default
  test_image_nil_profile_list
  test_image_empty_profile_list
  test_image_alternate_profile_list
}

test_profiles_project_images_profiles() {
  inc project create project1
  inc project switch project1
  storage=$(inc storage list | grep "^| " | tail -n 1 | cut -d' ' -f2)
  inc profile device add default root disk path=/ pool="$storage"

  test_image_nil_profile_list
  test_image_empty_profile_list
  test_image_alternate_profile_list

  inc project switch default
  inc project delete project1
}

# Run the tests with a project that only has the features.images enabled
test_profiles_project_images() {
  inc project create project1 -c features.profiles=false
  inc project switch project1

  test_image_nil_profile_list
  test_image_empty_profile_list
  test_image_alternate_profile_list

  inc project switch default
  inc project delete project1
}

test_profiles_project_profiles() {
  inc project create project1 -c features.images=false
  inc project switch project1
  storage=$(inc storage list | grep "^| " | tail -n 1 | cut -d' ' -f2)
  inc profile device add default root disk path=/ pool="$storage"

  test_image_nil_profile_list
  test_image_empty_profile_list
  test_image_alternate_profile_list

  inc project switch default
  inc project delete project1
}
