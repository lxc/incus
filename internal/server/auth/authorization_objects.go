package auth

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/mux"

	"github.com/lxc/incus/internal/version"
)

// Object is a string alias that represents an authorization object. These are formatted strings that
// uniquely identify an API resource, and can be constructed/deconstructed reliably.
// An Object is always of the form <ObjectType>:<identifier> where the identifier is a "/" delimited path containing elements that
// uniquely identify a resource. If the resource is defined at the project level, the first element of this path is always the project.
// Some example objects would be:
//   - `instance:default/c1`: Instance object in project "default" and name "c1".
//   - `storage_pool:local`: Storage pool object with name "local".
//   - `storage_volume:default/local/custom/vol1`: Storage volume object in project "default", storage pool "local", type "custom", and name "vol1".
type Object string

const (
	// objectTypeDelimiter is the string which separates the ObjectType from the remaining elements. Object types are
	// statically defined and do not contain this character, so we can extract the object type from an object by splitting
	// the string at this character.
	objectTypeDelimiter = ":"

	// objectElementDelimiter is the string which separates the elements of an object that make it a uniquely identifiable
	// resource. This was chosen because the character is not allowed in the majority of Incus resource names. Nevertheless
	// it is still necessary to escape this character in order to reliably construct/deconstruct an Object.
	objectElementDelimiter = "/"
)

// String implements fmt.Stringer for Object.
func (o Object) String() string {
	return string(o)
}

// Type returns the ObjectType of the Object.
func (o Object) Type() ObjectType {
	t, _, _ := strings.Cut(o.String(), objectTypeDelimiter)
	return ObjectType(t)
}

// Project returns the project of the Object if present.
func (o Object) Project() string {
	project, _ := o.projectAndElements()
	return project
}

// Elements returns the elements that uniquely identify the authorization Object.
func (o Object) Elements() []string {
	_, elements := o.projectAndElements()
	return elements
}

func (o Object) projectAndElements() (string, []string) {
	validator := objectValidators[o.Type()]
	_, identifier, _ := strings.Cut(o.String(), objectTypeDelimiter)

	var projectName string
	escapedObjectComponents := strings.SplitN(identifier, objectElementDelimiter, -1)
	components := make([]string, 0, len(escapedObjectComponents))
	for i, escapedComponent := range escapedObjectComponents {
		if validator.requireProject && i == 0 {
			projectName = unescape(escapedComponent)
			continue
		}

		components = append(components, unescape(escapedComponent))
	}

	return projectName, components
}

func (o Object) validate() error {
	objectType := o.Type()
	v, ok := objectValidators[objectType]
	if !ok {
		return fmt.Errorf("Missing validator for object of type %q", objectType)
	}

	projectName, identifierElements := o.projectAndElements()
	if v.requireProject && projectName == "" {
		return fmt.Errorf("Authorization objects of type %q require a project", objectType)
	}

	if len(identifierElements) != v.nIdentifierElements {
		return fmt.Errorf("Authorization objects of type %q require %d components to be uniquely identifiable", objectType, v.nIdentifierElements)
	}

	return nil
}

// objectValidator contains fields that can be used to determine if a string is a valid Object.
type objectValidator struct {
	nIdentifierElements int
	requireProject      bool
}

var objectValidators = map[ObjectType]objectValidator{
	ObjectTypeUser:          {nIdentifierElements: 1, requireProject: false},
	ObjectTypeServer:        {nIdentifierElements: 1, requireProject: false},
	ObjectTypeCertificate:   {nIdentifierElements: 1, requireProject: false},
	ObjectTypeStoragePool:   {nIdentifierElements: 1, requireProject: false},
	ObjectTypeProject:       {nIdentifierElements: 0, requireProject: true},
	ObjectTypeImage:         {nIdentifierElements: 1, requireProject: true},
	ObjectTypeImageAlias:    {nIdentifierElements: 1, requireProject: true},
	ObjectTypeInstance:      {nIdentifierElements: 1, requireProject: true},
	ObjectTypeNetwork:       {nIdentifierElements: 1, requireProject: true},
	ObjectTypeNetworkACL:    {nIdentifierElements: 1, requireProject: true},
	ObjectTypeNetworkZone:   {nIdentifierElements: 1, requireProject: true},
	ObjectTypeProfile:       {nIdentifierElements: 1, requireProject: true},
	ObjectTypeStorageBucket: {nIdentifierElements: 2, requireProject: true},
	ObjectTypeStorageVolume: {nIdentifierElements: 3, requireProject: true},
}

// NewObject returns an Object of the given type. The passed in arguments must be in the correct
// order (as found in the URL for the resource). This function will error if an invalid object type is
// given, or if the correct number of arguments is not passed in.
func NewObject(objectType ObjectType, projectName string, identifierElements ...string) (Object, error) {
	v, ok := objectValidators[objectType]
	if !ok {
		return "", fmt.Errorf("Missing validator for object of type %q", objectType)
	}

	if v.requireProject && projectName == "" {
		return "", fmt.Errorf("Authorization objects of type %q require a project", objectType)
	}

	if len(identifierElements) != v.nIdentifierElements {
		return "", fmt.Errorf("Authorization objects of type %q require %d components to be uniquely identifiable", objectType, v.nIdentifierElements)
	}

	builder := strings.Builder{}
	builder.WriteString(string(objectType))
	builder.WriteString(objectTypeDelimiter)
	if v.requireProject {
		builder.WriteString(escape(projectName))
		if len(identifierElements) > 0 {
			builder.WriteString(objectElementDelimiter)
		}
	}

	for i, c := range identifierElements {
		builder.WriteString(escape(c))
		if i != len(identifierElements)-1 {
			builder.WriteString(objectElementDelimiter)
		}
	}

	return Object(builder.String()), nil
}

// ObjectFromRequest returns an object created from the request by evaluating the given mux vars.
// Mux vars must be provided in the order that they are found in the endpoint path. If the object
// requires a project name, this is taken from the project query parameter unless the URL begins
// with /1.0/projects.
func ObjectFromRequest(r *http.Request, objectType ObjectType, muxVars ...string) (Object, error) {
	// Shortcut for server objects which don't require any arguments.
	if objectType == ObjectTypeServer {
		return ObjectServer(), nil
	}

	muxValues := make([]string, 0, len(muxVars))
	vars := mux.Vars(r)
	for _, muxVar := range muxVars {
		muxValue, err := url.PathUnescape(vars[muxVar])
		if err != nil {
			return "", fmt.Errorf("Failed to unescape mux var %q for object type %q: %w", muxVar, objectType, err)
		}

		if muxValue == "" {
			return "", fmt.Errorf("Mux var %q not found for object type %q", muxVar, objectType)
		}

		muxValues = append(muxValues, muxValue)
	}

	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", err
	}

	projectName := values.Get("project")
	if projectName == "" {
		projectName = "default"
	}

	// If using projects API we want to pass in the mux var, not the query parameter.
	if objectType == ObjectTypeProject && strings.HasPrefix(r.URL.Path, fmt.Sprintf("/%s/projects", version.APIVersion)) {
		if len(muxValues) == 0 {
			return "", fmt.Errorf("Missing project name path variable")
		}

		return ObjectProject(muxValues[0]), nil
	}

	return NewObject(objectType, projectName, muxValues...)
}

// ObjectFromString parses a string into an Object. It returns an error if the string is not valid.
func ObjectFromString(objectstr string) (Object, error) {
	o := Object(objectstr)
	err := o.validate()
	if err != nil {
		return "", err
	}

	return o, nil
}

func ObjectUser(userName string) Object {
	object, _ := NewObject(ObjectTypeUser, "", userName)
	return object
}

func ObjectServer() Object {
	object, _ := NewObject(ObjectTypeServer, "", "incus")
	return object
}

func ObjectCertificate(fingerprint string) Object {
	object, _ := NewObject(ObjectTypeCertificate, "", fingerprint)
	return object
}

func ObjectStoragePool(storagePoolName string) Object {
	object, _ := NewObject(ObjectTypeStoragePool, "", storagePoolName)
	return object
}

func ObjectProject(projectName string) Object {
	object, _ := NewObject(ObjectTypeProject, projectName)
	return object
}

func ObjectImage(projectName string, imageFingerprint string) Object {
	object, _ := NewObject(ObjectTypeImage, projectName, imageFingerprint)
	return object
}

func ObjectImageAlias(projectName string, aliasName string) Object {
	object, _ := NewObject(ObjectTypeImageAlias, projectName, aliasName)
	return object
}

func ObjectInstance(projectName string, instanceName string) Object {
	object, _ := NewObject(ObjectTypeInstance, projectName, instanceName)
	return object
}

func ObjectNetwork(projectName string, networkName string) Object {
	object, _ := NewObject(ObjectTypeNetwork, projectName, networkName)
	return object
}

func ObjectNetworkACL(projectName string, networkACLName string) Object {
	object, _ := NewObject(ObjectTypeNetworkACL, projectName, networkACLName)
	return object
}

func ObjectNetworkZone(projectName string, networkZoneName string) Object {
	object, _ := NewObject(ObjectTypeNetworkZone, projectName, networkZoneName)
	return object
}

func ObjectProfile(projectName string, profileName string) Object {
	object, _ := NewObject(ObjectTypeProfile, projectName, profileName)
	return object
}

func ObjectStorageBucket(projectName string, poolName string, bucketName string) Object {
	object, _ := NewObject(ObjectTypeStorageBucket, projectName, poolName, bucketName)
	return object
}

func ObjectStorageVolume(projectName string, poolName string, volumeType string, volumeName string) Object {
	object, _ := NewObject(ObjectTypeStorageVolume, projectName, poolName, volumeType, volumeName)
	return object
}

// escape escapes only the forward slash character as this is used as a delimiter. Everything else is allowed.
func escape(s string) string {
	return strings.Replace(s, "/", "%2F", -1)
}

// unescape replaces only the escaped forward slashes.
func unescape(s string) string {
	return strings.Replace(s, "%2F", "/", -1)
}
