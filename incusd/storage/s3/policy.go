package s3

import (
	"encoding/json"
	"fmt"
	"sort"
)

const roleAdmin = "admin"
const roleReadOnly = "read-only"

// Policy defines the S3 policy.
type Policy struct {
	Version   string
	Statement []PolicyStatement
}

// PolicyStatement defines the S3 policy statement.
type PolicyStatement struct {
	Effect   string
	Action   []string
	Resource []string
}

// BucketPolicy generates an S3 bucket policy for role.
func BucketPolicy(bucketName string, roleName string) (json.RawMessage, error) {
	switch roleName {
	case roleAdmin:
		return []byte(fmt.Sprintf(`{
			"Version": "2012-10-17",
			"Statement": [{
				"Effect": "Allow",
				"Action": [
					"s3:*"
				],
				"Resource": [
					"arn:aws:s3:::%s/*"
				]
			}]
		}`, bucketName)), nil
	case roleReadOnly:
		return []byte(fmt.Sprintf(`{
			"Version": "2012-10-17",
			"Statement": [{
				"Effect": "Allow",
				"Action": [
					"s3:ListBucket",
					"s3:GetBucketLocation",
					"s3:GetObject",
					"s3:GetObjectVersion"
				],
				"Resource": [
					"arn:aws:s3:::%s/*"
				]
			}]
		}`, bucketName)), nil
	}

	return nil, fmt.Errorf("Invalid key role")
}

// BucketPolicyRole compares the given bucket policy with the predefined bucket policies
// and returns the role name of the matching policy.
func BucketPolicyRole(bucketName string, jsonPolicy string) (string, error) {
	var policy Policy

	err := json.Unmarshal([]byte(jsonPolicy), &policy)
	if err != nil {
		return "", err
	}

	predefinedRoles := []string{roleAdmin, roleReadOnly}
	for _, role := range predefinedRoles {
		var rolePolicy Policy

		jsonRolePolicy, err := BucketPolicy(bucketName, role)
		if err != nil {
			return "", err
		}

		err = json.Unmarshal([]byte(jsonRolePolicy), &rolePolicy)
		if err != nil {
			return "", err
		}

		matches := comparePolicy(policy, rolePolicy)
		if matches {
			return role, nil
		}
	}

	return "", fmt.Errorf("Policy does not match any role")
}

// comparePolicy checks whether two policies are equal.
func comparePolicy(policyA Policy, policyB Policy) bool {
	if policyA.Version != policyB.Version {
		return false
	}

	if len(policyA.Statement) != len(policyB.Statement) {
		return false
	}

	for i := range policyA.Statement {
		psA := policyA.Statement[i]
		psB := policyB.Statement[i]

		if psA.Effect != psB.Effect {
			return false
		}

		if len(psA.Action) != len(psB.Action) {
			return false
		}

		if len(psA.Resource) != len(psB.Resource) {
			return false
		}

		sort.Strings(psA.Action)
		sort.Strings(psB.Action)

		for j := range psA.Action {
			if psA.Action[j] != psB.Action[j] {
				return false
			}
		}

		sort.Strings(psB.Resource)
		sort.Strings(psB.Resource)

		for j := range psA.Resource {
			if psA.Resource[j] != psB.Resource[j] {
				return false
			}
		}
	}

	return true
}
