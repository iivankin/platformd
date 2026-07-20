package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/domainvariables"
	"github.com/iivankin/platformd/internal/managedpostgres"
	"github.com/iivankin/platformd/internal/managedredis"
	"github.com/iivankin/platformd/internal/objectstore"
	"github.com/iivankin/platformd/internal/resourcevariables"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/variableexpression"
)

type resourceVariableResolver struct {
	store  *state.Store
	master cryptobox.MasterKey
}

type environmentResolution struct {
	resolver  resourceVariableResolver
	projectID string
	resources map[string]state.ProjectResource
	services  map[string]state.ServiceDesired
	cache     map[string]string
	resolving map[string]bool
}

func (resolver resourceVariableResolver) Resolve(ctx context.Context, desired state.ServiceDesired, deploymentID string) (map[string]string, error) {
	resources, err := resolver.store.ProjectResources(ctx, desired.ProjectID)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]state.ProjectResource, len(resources))
	for _, resource := range resources {
		byName[resource.Name] = resource
	}
	resolution := environmentResolution{
		resolver: resolver, projectID: desired.ProjectID, resources: byName,
		services: map[string]state.ServiceDesired{desired.ID: desired},
		cache:    make(map[string]string), resolving: make(map[string]bool),
	}
	result := make(map[string]string, len(desired.Snapshot.Environment))
	for name := range desired.Snapshot.Environment {
		value, resolveErr := resolution.serviceVariable(ctx, desired, name)
		if resolveErr != nil {
			return nil, fmt.Errorf("%s: %w", name, resolveErr)
		}
		result[name] = value
	}
	domains, err := resolver.store.ServiceDomains(ctx, desired.ProjectID, desired.ID)
	if err != nil {
		return nil, err
	}
	publicURLs := make([]string, 0, len(domains))
	for _, domain := range domains {
		publicURLs = append(publicURLs, "https://"+domain.Hostname)
	}
	sort.Strings(publicURLs)
	result["PLATFORMD_PROJECT_ID"] = desired.ProjectID
	result["PLATFORMD_PROJECT_NAME"] = desired.ProjectName
	result["PLATFORMD_SERVICE_ID"] = desired.ID
	result["PLATFORMD_SERVICE_NAME"] = desired.Name
	result["PLATFORMD_DEPLOYMENT_ID"] = deploymentID
	result["PLATFORMD_PRIVATE_DOMAIN"] = desired.Name + "." + desired.ProjectName + ".internal"
	result["PLATFORMD_PUBLIC_URLS"] = strings.Join(publicURLs, ",")
	return result, nil
}

func (resolution *environmentResolution) serviceVariable(ctx context.Context, service state.ServiceDesired, name string) (string, error) {
	key := service.ID + "." + name
	if value, ok := resolution.cache[key]; ok {
		return value, nil
	}
	if resolution.resolving[key] {
		return "", fmt.Errorf("variable reference cycle at %s.%s", service.Name, name)
	}
	raw, ok := service.Snapshot.Environment[name]
	if !ok {
		return "", fmt.Errorf("service %s does not export %s", service.Name, name)
	}
	resolution.resolving[key] = true
	defer delete(resolution.resolving, key)
	value, err := variableexpression.Expand(raw, func(reference variableexpression.Reference) (string, error) {
		return resolution.reference(ctx, reference)
	})
	if err != nil {
		return "", err
	}
	resolution.cache[key] = value
	return value, nil
}

func (resolution *environmentResolution) reference(ctx context.Context, reference variableexpression.Reference) (string, error) {
	resource, ok := resolution.resources[reference.Resource]
	if !ok {
		return "", fmt.Errorf("resource %s does not exist in this project", reference.Resource)
	}
	cacheKey := resource.ID + "." + reference.Output
	if value, ok := resolution.cache[cacheKey]; ok {
		return value, nil
	}
	var value string
	var err error
	switch resource.Kind {
	case "service":
		service, loadErr := resolution.service(ctx, resource.ID)
		if loadErr != nil {
			return "", loadErr
		}
		value, err = resolution.serviceOutput(ctx, service, reference.Output)
	case "postgres":
		if !resourcevariables.Supports("postgres", reference.Output) {
			return "", fmt.Errorf("PostgreSQL %s does not export %s", resource.Name, reference.Output)
		}
		postgres, loadErr := resolution.resolver.store.ManagedPostgresInProject(ctx, resolution.projectID, resource.ID)
		if loadErr != nil {
			return "", loadErr
		}
		password, openErr := managedpostgres.OpenOwnerPassword(resolution.resolver.master, postgres.ID, postgres.OwnerPasswordEncrypted)
		if openErr != nil {
			return "", openErr
		}
		value, err = postgresOutput(postgres, password, reference.Output)
	case "redis":
		if !resourcevariables.Supports("redis", reference.Output) {
			return "", fmt.Errorf("Redis %s does not export %s", resource.Name, reference.Output)
		}
		redis, loadErr := resolution.resolver.store.ManagedRedisInProject(ctx, resolution.projectID, resource.ID)
		if loadErr != nil {
			return "", loadErr
		}
		password, openErr := managedredis.OpenPassword(resolution.resolver.master, redis.ID, redis.PasswordEncrypted)
		if openErr != nil {
			return "", openErr
		}
		value, err = redisOutput(redis, password, reference.Output)
	case "object_store":
		if !resourcevariables.Supports("object_store", reference.Output) {
			return "", fmt.Errorf("object store %s does not export %s", resource.Name, reference.Output)
		}
		value, err = resolution.objectStoreOutput(ctx, resource.ID, reference.Output)
	case "network_gateway":
		if !resourcevariables.Supports("network_gateway", reference.Output) {
			return "", fmt.Errorf("network gateway %s does not export %s", resource.Name, reference.Output)
		}
		gateway, loadErr := resolution.resolver.store.NetworkGateway(ctx, resolution.projectID, resource.ID)
		if loadErr != nil {
			return "", loadErr
		}
		value, err = networkGatewayOutput(gateway, reference.Output)
	default:
		return "", fmt.Errorf("unsupported resource kind %s", resource.Kind)
	}
	if err != nil {
		return "", err
	}
	resolution.cache[cacheKey] = value
	return value, nil
}

func (resolution *environmentResolution) service(ctx context.Context, serviceID string) (state.ServiceDesired, error) {
	if service, ok := resolution.services[serviceID]; ok {
		return service, nil
	}
	service, err := resolution.resolver.store.Service(ctx, resolution.projectID, serviceID)
	if err != nil {
		return state.ServiceDesired{}, err
	}
	resolution.services[serviceID] = service
	return service, nil
}

func (resolution *environmentResolution) serviceOutput(ctx context.Context, service state.ServiceDesired, output string) (string, error) {
	if _, ok := service.Snapshot.Environment[output]; ok {
		return resolution.serviceVariable(ctx, service, output)
	}
	domains, err := resolution.resolver.store.ServiceDomains(ctx, resolution.projectID, service.ID)
	if err != nil {
		return "", err
	}
	values := make(map[string]string, len(domains)*2)
	for _, domain := range domains {
		names, nameErr := domainvariables.OutputNames(domain.Hostname)
		if nameErr != nil {
			return "", nameErr
		}
		for name, value := range map[string]string{
			names.Public:   "https://" + domain.Hostname,
			names.Internal: "http://" + service.Name + "." + service.ProjectName + ".internal:" + strconv.Itoa(domain.TargetPort),
		} {
			if _, duplicate := values[name]; duplicate {
				return "", fmt.Errorf("service %s has ambiguous domain output %s", service.Name, name)
			}
			values[name] = value
		}
	}
	value, ok := values[output]
	if !ok {
		return "", fmt.Errorf("service %s does not export %s", service.Name, output)
	}
	return value, nil
}

func (resolution *environmentResolution) objectStoreOutput(ctx context.Context, resourceID, output string) (string, error) {
	resource, err := resolution.resolver.store.ObjectStoreInProject(ctx, resolution.projectID, resourceID)
	if err != nil {
		return "", err
	}
	credentials, err := resolution.resolver.store.S3CredentialsByObjectStore(ctx, resource.ID)
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
	secret, err := objectstore.OpenSecret(resolution.resolver.master, resource.ID, credential.ID, credential.SecretEncrypted)
	if err != nil {
		return "", err
	}
	return objectStoreOutput(resource, accessKey, secret, output)
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
	values["POSTGRES_URL"] = connection.String()
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

func networkGatewayOutput(resource state.NetworkGateway, output string) (string, error) {
	if resource.Mode != "import" {
		return "", fmt.Errorf("export gateway %s has no internal endpoint", resource.Name)
	}
	host := resource.Name + "." + resource.ProjectName + ".internal"
	values := map[string]string{
		"HOST": host, "PORT": strconv.Itoa(resource.ListenPort),
		"ADDRESS": net.JoinHostPort(host, strconv.Itoa(resource.ListenPort)),
	}
	value, ok := values[output]
	if !ok {
		return "", fmt.Errorf("unsupported network gateway output %s", output)
	}
	return value, nil
}
