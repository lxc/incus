package drivers

// S3Credentials represents the credentials to access a bucket.
type S3Credentials struct {
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
}
