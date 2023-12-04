package auth

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/suite"

	localtls "github.com/lxc/incus/shared/tls"
)

type objectSuite struct {
	suite.Suite
}

func TestObjectSuite(t *testing.T) {
	suite.Run(t, new(objectSuite))
}

func (s *objectSuite) TestObjectCertificate() {
	s.Assert().NotPanics(func() {
		fingerprint := localtls.TestingKeyPair().Fingerprint()
		o := ObjectCertificate(fingerprint)
		s.Equal(fmt.Sprintf("certificate:%s", fingerprint), string(o))
	})
}

func (s *objectSuite) TestObjectImage() {
	s.Assert().NotPanics(func() {
		fingerprint := localtls.TestingKeyPair().Fingerprint()
		o := ObjectImage("default", fingerprint)
		s.Equal(fmt.Sprintf("image:default/%s", fingerprint), string(o))
	})
}

func (s *objectSuite) TestObjectImageAlias() {
	s.Assert().NotPanics(func() {
		o := ObjectImageAlias("default", "image_alias_name")
		s.Equal("image_alias:default/image_alias_name", string(o))
	})
}

func (s *objectSuite) TestObjectInstance() {
	s.Assert().NotPanics(func() {
		o := ObjectInstance("default", "instance_name")
		s.Equal("instance:default/instance_name", string(o))
	})
}

func (s *objectSuite) TestObjectNetwork() {
	s.Assert().NotPanics(func() {
		o := ObjectNetwork("default", "network_name")
		s.Equal("network:default/network_name", string(o))
	})
}

func (s *objectSuite) TestObjectNetworkACL() {
	s.Assert().NotPanics(func() {
		o := ObjectNetworkACL("default", "network_acl_name")
		s.Equal("network_acl:default/network_acl_name", string(o))
	})
}

func (s *objectSuite) TestObjectNetworkZone() {
	s.Assert().NotPanics(func() {
		o := ObjectNetworkZone("default", "network_zone_name")
		s.Equal("network_zone:default/network_zone_name", string(o))
	})
}

func (s *objectSuite) TestObjectProfile() {
	s.Assert().NotPanics(func() {
		o := ObjectProfile("default", "profile_name")
		s.Equal("profile:default/profile_name", string(o))
	})
}

func (s *objectSuite) TestObjectProject() {
	s.Assert().NotPanics(func() {
		o := ObjectProject("default")
		s.Equal("project:default", string(o))
	})
}

func (s *objectSuite) TestObjectServer() {
	s.Assert().NotPanics(func() {
		o := ObjectServer()
		s.Equal("server:incus", string(o))
	})
}

func (s *objectSuite) TestObjectStorageBucket() {
	s.Assert().NotPanics(func() {
		o := ObjectStorageBucket("default", "pool_name", "storage_bucket_name", "")
		s.Equal("storage_bucket:default/pool_name/storage_bucket_name", string(o))
	})

	s.Assert().NotPanics(func() {
		o := ObjectStorageBucket("default", "pool_name", "storage_bucket_name", "location")
		s.Equal("storage_bucket:default/pool_name/storage_bucket_name/location", string(o))
	})
}

func (s *objectSuite) TestObjectStoragePool() {
	s.Assert().NotPanics(func() {
		o := ObjectStoragePool("pool_name")
		s.Equal("storage_pool:pool_name", string(o))
	})
}

func (s *objectSuite) TestObjectStorageVolume() {
	s.Assert().NotPanics(func() {
		o := ObjectStorageVolume("default", "pool_name", "volume_type", "volume_name", "")
		s.Equal("storage_volume:default/pool_name/volume_type/volume_name", string(o))
	})

	s.Assert().NotPanics(func() {
		o := ObjectStorageVolume("default", "pool_name", "volume_type", "volume_name", "location")
		s.Equal("storage_volume:default/pool_name/volume_type/volume_name/location", string(o))
	})
}

func (s *objectSuite) TestObjectUser() {
	s.Assert().NotPanics(func() {
		o := ObjectUser("username")
		s.Equal("user:username", string(o))
	})
}

func (s *objectSuite) TestObjectFromString() {
	tests := []struct {
		in  string
		out Object
		err error
	}{
		{
			in:  "server:incus",
			out: Object("server:incus"),
		},
		{
			in:  "certificate:weaowiejfoiawefpajewfpoawjfepojawef",
			out: Object("certificate:weaowiejfoiawefpajewfpoawjfepojawef"),
		},
		{
			in:  "storage_pool:local",
			out: Object("storage_pool:local"),
		},
		{
			in:  "project:default",
			out: Object("project:default"),
		},
		{
			in:  "profile:default/default",
			out: Object("profile:default/default"),
		},
		{
			in:  "image:default/eoaiwenfoaiwnefoianwef",
			out: Object("image:default/eoaiwenfoaiwnefoianwef"),
		},
		{
			in:  "image_alias:default/windows11",
			out: Object("image_alias:default/windows11"),
		},
		{
			in:  "network:default/incusbr0",
			out: Object("network:default/incusbr0"),
		},
		{
			in:  "network_acl:default/acl1",
			out: Object("network_acl:default/acl1"),
		},
		{
			in:  "network_zone:default/example.com",
			out: Object("network_zone:default/example.com"),
		},
		{
			in:  "storage_volume:default/local/custom/vol1",
			out: Object("storage_volume:default/local/custom/vol1"),
		},
		{
			in:  "storage_bucket:default/local/bucket1",
			out: Object("storage_bucket:default/local/bucket1"),
		},
	}

	for _, tt := range tests {
		o, err := ObjectFromString(tt.in)
		s.Equal(tt.err, err)
		s.Equal(tt.out, o)
	}
}

// Objects shouldn't continuously path escape.
func (s *objectSuite) TestRemake() {
	o := ObjectProject("contains/forward/slashes")
	oSquared, err := ObjectFromString(o.String())
	s.Nil(err)
	s.Equal(o.String(), oSquared.String())
}
