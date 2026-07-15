package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/managedpostgres"
	"github.com/iivankin/platformd/internal/managedredis"
	"github.com/iivankin/platformd/internal/objectstore"
	"github.com/iivankin/platformd/internal/resourcevariables"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
)

type resourceVariableResolver struct {
	store  *state.Store
	master cryptobox.MasterKey
}

func (resolver resourceVariableResolver) Resolve(ctx context.Context, desired state.ServiceDesired) (map[string]string, error) {
	result := make(map[string]string, len(desired.Snapshot.ResourceReferences))
	for _, reference := range desired.Snapshot.ResourceReferences {
		value, err := resolver.resolve(ctx, desired, reference)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", reference.EnvironmentName, err)
		}
		result[reference.EnvironmentName] = value
	}
	return result, nil
}

func (resolver resourceVariableResolver) resolve(ctx context.Context, desired state.ServiceDesired, reference serviceconfig.ResourceReference) (string, error) {
	if !resourcevariables.Supports(reference.ResourceKind, reference.OutputName) {
		return "", fmt.Errorf("%s does not export %s", reference.ResourceKind, reference.OutputName)
	}
	switch reference.ResourceKind {
	case "service":
		resource, err := resolver.store.Service(ctx, desired.ProjectID, reference.ResourceID)
		if err != nil {
			return "", err
		}
		host := resource.Name + "." + resource.ProjectName + ".internal"
		return serviceOutput(resource, host, reference.OutputName)
	case "postgres":
		resource, err := resolver.store.ManagedPostgresInProject(ctx, desired.ProjectID, reference.ResourceID)
		if err != nil {
			return "", err
		}
		password, err := managedpostgres.OpenOwnerPassword(resolver.master, resource.ID, resource.OwnerPasswordEncrypted)
		if err != nil {
			return "", err
		}
		return postgresOutput(resource, password, reference.OutputName)
	case "redis":
		resource, err := resolver.store.ManagedRedisInProject(ctx, desired.ProjectID, reference.ResourceID)
		if err != nil {
			return "", err
		}
		password, err := managedredis.OpenPassword(resolver.master, resource.ID, resource.PasswordEncrypted)
		if err != nil {
			return "", err
		}
		return redisOutput(resource, password, reference.OutputName)
	case "object_store":
		resource, err := resolver.store.ObjectStoreInProject(ctx, desired.ProjectID, reference.ResourceID)
		if err != nil {
			return "", err
		}
		credentials, err := resolver.store.S3CredentialsByObjectStore(ctx, resource.ID)
		if err != nil {
			return "", err
		}
		if len(credentials) != 1 {
			return "", errors.New("object store must have exactly one active credential")
		}
		credential := credentials[0]
		accessKey, err := objectstore.AccessKeyID(credential.ID)
		if err != nil {
			return "", err
		}
		secret, err := objectstore.OpenSecret(resolver.master, resource.ID, credential.ID, credential.SecretEncrypted)
		if err != nil {
			return "", err
		}
		return objectStoreOutput(resource, accessKey, secret, reference.OutputName)
	default:
		return "", fmt.Errorf("unsupported resource kind %s", reference.ResourceKind)
	}
}

func serviceOutput(resource state.ServiceDesired, host, output string) (string, error) {
	switch output {
	case "HOST":
		return host, nil
	case "PORT", "URL":
		if resource.Snapshot.TargetPort == nil {
			return "", errors.New("referenced service has no target port")
		}
		port := strconv.Itoa(*resource.Snapshot.TargetPort)
		if output == "PORT" {
			return port, nil
		}
		return "http://" + host + ":" + port, nil
	default:
		return "", fmt.Errorf("unsupported service output %s", output)
	}
}

func postgresOutput(resource state.ManagedPostgres, password, output string) (string, error) {
	host := resource.Name + "." + resource.ProjectName + ".internal"
	values := map[string]string{
		"PGHOST": host, "PGPORT": "5432", "PGDATABASE": resource.DatabaseName,
		"PGUSER": resource.OwnerUsername, "PGPASSWORD": password,
	}
	connection := &url.URL{Scheme: "postgresql", Host: host + ":5432", Path: "/" + resource.DatabaseName}
	connection.User = url.UserPassword(resource.OwnerUsername, password)
	values["DATABASE_URL"] = connection.String()
	value, ok := values[output]
	if !ok {
		return "", fmt.Errorf("unsupported PostgreSQL output %s", output)
	}
	return value, nil
}

func redisOutput(resource state.ManagedRedis, password, output string) (string, error) {
	host := resource.Name + "." + resource.ProjectName + ".internal"
	connection := &url.URL{Scheme: "redis", Host: host + ":6379", Path: "/0", User: url.UserPassword("", password)}
	values := map[string]string{
		"REDISHOST": host, "REDISPORT": "6379", "REDISPASSWORD": password, "REDIS_URL": connection.String(),
	}
	value, ok := values[output]
	if !ok {
		return "", fmt.Errorf("unsupported Redis output %s", output)
	}
	return value, nil
}

func objectStoreOutput(resource state.ObjectStore, accessKey, secret, output string) (string, error) {
	values := map[string]string{
		"S3_ENDPOINT": "http://" + resource.Name + "." + resource.ProjectName + ".internal:9000",
		"S3_REGION":   objectstore.Region, "S3_BUCKET": resource.BucketName,
		"S3_ACCESS_KEY_ID": accessKey, "S3_SECRET_ACCESS_KEY": secret,
	}
	value, ok := values[output]
	if !ok {
		return "", fmt.Errorf("unsupported object store output %s", output)
	}
	return value, nil
}
