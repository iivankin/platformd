package resourcevariables

import "slices"

var outputs = map[string][]string{
	"postgres":        {"PGHOST", "PGPORT", "PGDATABASE", "PGUSER", "PGPASSWORD", "DATABASE_URL", "POSTGRES_URL"},
	"redis":           {"REDISHOST", "REDISPORT", "REDISPASSWORD", "REDIS_URL"},
	"object_store":    {"S3_ENDPOINT", "S3_REGION", "S3_BUCKET", "S3_ACCESS_KEY_ID", "S3_SECRET_ACCESS_KEY"},
	"network_gateway": {"HOST", "PORT", "ADDRESS"},
	"service": {
		"PLATFORMD_PROJECT_ID", "PLATFORMD_PROJECT_NAME", "PLATFORMD_SERVICE_ID",
		"PLATFORMD_SERVICE_NAME", "PLATFORMD_PRIVATE_DOMAIN", "PLATFORMD_PUBLIC_URLS",
	},
}

func OutputNames(kind string) []string {
	return append([]string(nil), outputs[kind]...)
}

func Supports(kind, outputName string) bool {
	return slices.Contains(outputs[kind], outputName)
}
