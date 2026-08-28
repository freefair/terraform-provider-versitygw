package provider

import "regexp"

// bucketNamePattern is the S3 naming rule the gateway inherits: lower case,
// 3-63 characters, starting and ending alphanumerically. Length is checked
// separately so the two failures produce distinct messages.
var bucketNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*[a-z0-9]$`)
