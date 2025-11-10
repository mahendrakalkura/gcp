package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	aiplatform "cloud.google.com/go/aiplatform/apiv1"
	"cloud.google.com/go/aiplatform/apiv1/aiplatformpb"
	artifactregistry "cloud.google.com/go/artifactregistry/apiv1"
	"cloud.google.com/go/artifactregistry/apiv1/artifactregistrypb"
	"cloud.google.com/go/bigquery"
	billing "cloud.google.com/go/billing/apiv1"
	"cloud.google.com/go/billing/apiv1/billingpb"
	cloudbuild "cloud.google.com/go/cloudbuild/apiv1/v2"
	"cloud.google.com/go/cloudbuild/apiv1/v2/cloudbuildpb"
	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	functions "cloud.google.com/go/functions/apiv1"
	"cloud.google.com/go/functions/apiv1/functionspb"
	"cloud.google.com/go/pubsub"
	redis "cloud.google.com/go/redis/apiv1"
	"cloud.google.com/go/redis/apiv1/redispb"
	run "cloud.google.com/go/run/apiv2"
	"cloud.google.com/go/run/apiv2/runpb"
	"cloud.google.com/go/storage"
	"google.golang.org/api/container/v1"
	"google.golang.org/api/dns/v1"
	"google.golang.org/api/iterator"
	sqladmin "google.golang.org/api/sqladmin/v1"
)

type ServiceAccount struct {
	Type                    string `json:"type"`
	ProjectID               string `json:"project_id"`
	PrivateKeyID            string `json:"private_key_id"`
	PrivateKey              string `json:"private_key"`
	ClientEmail             string `json:"client_email"`
	ClientID                string `json:"client_id"`
	AuthURI                 string `json:"auth_uri"`
	TokenURI                string `json:"token_uri"`
	AuthProviderX509CertURL string `json:"auth_provider_x509_cert_url"`
	ClientX509CertURL       string `json:"client_x509_cert_url"`
}

type Resource struct {
	Type         string
	Name         string
	Location     string
	Status       string
	Details      string
	MonthlyCost  float64
	CostCurrency string
}

type ResourceResult struct {
	Resources []Resource
	Error     error
	Service   string
}

type ErrorSummary struct {
	Service string
	Error   error
}

type Cache struct {
	Resources   []Resource
	Timestamp   time.Time
	ProjectID   string
	TotalCost   float64
	mu          sync.RWMutex
	cacheFile   string
	cacheExpiry time.Duration
}

func main() {
	ctx := context.Background()

	// Check if google.json exists
	if _, err := os.Stat("google.json"); os.IsNotExist(err) {
		log.Fatal("google.json file not found in current directory")
	}

	// Set credentials
	os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "google.json")

	// Get project ID from credentials file
	projectID, err := getProjectIDFromJSON("google.json")
	if err != nil {
		log.Fatalf("Failed to get project ID: %v", err)
	}

	fmt.Printf("Scanning GCP resources for project: %s\n", projectID)

	// Check cache first
	cache := NewCache(".gcp-cache.json", 5*time.Minute)
	if cachedData, valid := cache.Get(projectID); valid {
		fmt.Printf("\n✓ Using cached data from %s ago\n", time.Since(cachedData.Timestamp).Round(time.Second))
		displayResourceTable(cachedData.Resources, cachedData.TotalCost)
		return
	}

	fmt.Println("Fetching resources in parallel...")

	// Parallel resource fetching
	var wg sync.WaitGroup
	resultsChan := make(chan ResourceResult, 22)
	errors := []ErrorSummary{}
	var errorsMu sync.Mutex

	services := []struct {
		name string
		fn   func(context.Context, string) ([]Resource, error)
	}{
		{"Compute Engine Instances", listComputeInstances},
		{"Compute Engine Disks", listComputeDisks},
		{"Cloud Storage Buckets", listStorageBuckets},
		{"Cloud SQL Instances", listCloudSQLInstances},
		{"GKE Clusters", listGKEClusters},
		{"BigQuery Datasets", listBigQueryDatasets},
		{"Cloud Functions", listCloudFunctions},
		{"Cloud Run Services", listCloudRunServices},
		{"Pub/Sub Topics", listPubSubTopics},
		{"Load Balancers", listLoadBalancers},
		{"VPN Gateways", listVPNGateways},
		{"Cloud NAT", listCloudNAT},
		{"Memorystore Redis", listMemorystore},
		{"Cloud DNS Zones", listCloudDNS},
		{"Reserved IP Addresses", listReservedIPs},
		{"Cloud Build Triggers", listCloudBuildTriggers},
		{"Cloud Build Runs", listCloudBuildRuns},
		{"Artifact Registry Repositories", listArtifactRegistryRepos},
		{"Vertex AI Models", listVertexAIModels},
		{"Vertex AI Endpoints", listVertexAIEndpoints},
		{"Vertex AI Custom Jobs", listVertexAICustomJobs},
	}

	for _, svc := range services {
		wg.Add(1)
		go func(serviceName string, fetchFunc func(context.Context, string) ([]Resource, error)) {
			defer wg.Done()
			resources, err := fetchFunc(ctx, projectID)
			resultsChan <- ResourceResult{
				Resources: resources,
				Error:     err,
				Service:   serviceName,
			}
		}(svc.name, svc.fn)
	}

	// Close channel when all goroutines complete
	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	// Collect results
	var allResources []Resource
	for result := range resultsChan {
		if result.Error != nil {
			// Only show errors that are NOT "API not enabled" type errors
			if !isAPINotEnabledError(result.Error) {
				errorsMu.Lock()
				errors = append(errors, ErrorSummary{Service: result.Service, Error: result.Error})
				errorsMu.Unlock()
				fmt.Printf("⚠ %s: %v\n", result.Service, result.Error)
			}
		} else {
			fmt.Printf("✓ %s: found %d resources\n", result.Service, len(result.Resources))
			allResources = append(allResources, result.Resources...)
		}
	}

	// Fetch cost estimates
	fmt.Println("\nFetching cost estimates...")
	allResources, totalCost := estimateCosts(ctx, projectID, allResources)

	// Cache the results
	cache.Set(projectID, allResources, totalCost)

	// Display results
	fmt.Printf("\n\nFound %d billable resources:\n\n", len(allResources))
	displayResourceTable(allResources, totalCost)

	// Display error summary if any
	if len(errors) > 0 {
		fmt.Printf("\n\n⚠ Errors encountered (%d services):\n", len(errors))
		for _, e := range errors {
			fmt.Printf("  • %s: %v\n", e.Service, e.Error)
		}
	}
}

// Get project ID from google.json file
func getProjectIDFromJSON(filename string) (string, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return "", fmt.Errorf("failed to read credentials file: %w", err)
	}

	var sa ServiceAccount
	if err := json.Unmarshal(data, &sa); err != nil {
		return "", fmt.Errorf("failed to parse credentials file: %w", err)
	}

	if sa.ProjectID == "" {
		return "", fmt.Errorf("project_id not found in credentials file")
	}

	return sa.ProjectID, nil
}

func listComputeInstances(ctx context.Context, projectID string) ([]Resource, error) {
	var resources []Resource

	client, err := compute.NewInstancesRESTClient(ctx)
	if err != nil {
		return resources, retryableError(err, "create compute client")
	}
	defer client.Close()

	req := &computepb.AggregatedListInstancesRequest{
		Project: projectID,
	}

	it := client.AggregatedList(ctx, req)
	for {
		pair, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, retryableError(err, "list instances")
		}

		for _, instance := range pair.Value.Instances {
			status := "UNKNOWN"
			if instance.Status != nil {
				status = *instance.Status
			}

			machineType := "unknown"
			if instance.MachineType != nil {
				parts := strings.Split(*instance.MachineType, "/")
				machineType = parts[len(parts)-1]
			}

			resources = append(resources, Resource{
				Type:     "Compute Engine VM",
				Name:     getStringValue(instance.Name),
				Location: pair.Key,
				Status:   status,
				Details:  fmt.Sprintf("Type: %s", machineType),
			})
		}
	}

	return resources, nil
}

func listComputeDisks(ctx context.Context, projectID string) ([]Resource, error) {
	var resources []Resource

	client, err := compute.NewDisksRESTClient(ctx)
	if err != nil {
		return resources, retryableError(err, "create disks client")
	}
	defer client.Close()

	req := &computepb.AggregatedListDisksRequest{
		Project: projectID,
	}

	it := client.AggregatedList(ctx, req)
	for {
		pair, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, retryableError(err, "list disks")
		}

		for _, disk := range pair.Value.Disks {
			size := int64(0)
			if disk.SizeGb != nil {
				size = *disk.SizeGb
			}

			status := "UNKNOWN"
			if disk.Status != nil {
				status = *disk.Status
			}

			resources = append(resources, Resource{
				Type:     "Persistent Disk",
				Name:     getStringValue(disk.Name),
				Location: pair.Key,
				Status:   status,
				Details:  fmt.Sprintf("Size: %d GB", size),
			})
		}
	}

	return resources, nil
}

func listStorageBuckets(ctx context.Context, projectID string) ([]Resource, error) {
	var resources []Resource

	client, err := storage.NewClient(ctx)
	if err != nil {
		return resources, retryableError(err, "create storage client")
	}
	defer client.Close()

	it := client.Buckets(ctx, projectID)
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, retryableError(err, "list buckets")
		}

		resources = append(resources, Resource{
			Type:     "Cloud Storage Bucket",
			Name:     attrs.Name,
			Location: attrs.Location,
			Status:   "ACTIVE",
			Details:  fmt.Sprintf("Class: %s", attrs.StorageClass),
		})
	}

	return resources, nil
}

func listCloudSQLInstances(ctx context.Context, projectID string) ([]Resource, error) {
	var resources []Resource

	service, err := sqladmin.NewService(ctx)
	if err != nil {
		return resources, retryableError(err, "create SQL admin client")
	}

	resp, err := service.Instances.List(projectID).Do()
	if err != nil {
		return resources, retryableError(err, "list Cloud SQL instances")
	}

	for _, instance := range resp.Items {
		resources = append(resources, Resource{
			Type:     "Cloud SQL Instance",
			Name:     instance.Name,
			Location: instance.Region,
			Status:   instance.State,
			Details:  fmt.Sprintf("Version: %s, Tier: %s", instance.DatabaseVersion, instance.Settings.Tier),
		})
	}

	return resources, nil
}

func listGKEClusters(ctx context.Context, projectID string) ([]Resource, error) {
	var resources []Resource

	service, err := container.NewService(ctx)
	if err != nil {
		return resources, retryableError(err, "create container client")
	}

	parent := fmt.Sprintf("projects/%s/locations/-", projectID)
	resp, err := service.Projects.Locations.Clusters.List(parent).Do()
	if err != nil {
		return resources, retryableError(err, "list GKE clusters")
	}

	for _, cluster := range resp.Clusters {
		nodeCount := 0
		for _, pool := range cluster.NodePools {
			nodeCount += int(pool.InitialNodeCount)
		}

		resources = append(resources, Resource{
			Type:     "GKE Cluster",
			Name:     cluster.Name,
			Location: cluster.Location,
			Status:   cluster.Status,
			Details:  fmt.Sprintf("Nodes: %d, Version: %s", nodeCount, cluster.CurrentMasterVersion),
		})
	}

	return resources, nil
}

func listBigQueryDatasets(ctx context.Context, projectID string) ([]Resource, error) {
	var resources []Resource

	client, err := bigquery.NewClient(ctx, projectID)
	if err != nil {
		return resources, retryableError(err, "create BigQuery client")
	}
	defer client.Close()

	it := client.Datasets(ctx)
	for {
		dataset, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, retryableError(err, "list datasets")
		}

		metadata, err := dataset.Metadata(ctx)
		if err != nil {
			log.Printf("Error getting dataset metadata: %v", err)
			continue
		}

		resources = append(resources, Resource{
			Type:     "BigQuery Dataset",
			Name:     dataset.DatasetID,
			Location: metadata.Location,
			Status:   "ACTIVE",
			Details:  "Dataset storage",
		})
	}

	return resources, nil
}

func listCloudFunctions(ctx context.Context, projectID string) ([]Resource, error) {
	var resources []Resource

	client, err := functions.NewCloudFunctionsClient(ctx)
	if err != nil {
		return resources, retryableError(err, "create functions client")
	}
	defer client.Close()

	req := &functionspb.ListFunctionsRequest{
		Parent: fmt.Sprintf("projects/%s/locations/-", projectID),
	}

	it := client.ListFunctions(ctx, req)
	for {
		fn, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, retryableError(err, "list functions")
		}

		resources = append(resources, Resource{
			Type:     "Cloud Function",
			Name:     extractResourceName(fn.Name),
			Location: extractLocation(fn.Name),
			Status:   fn.Status.String(),
			Details:  fmt.Sprintf("Runtime: %s", fn.Runtime),
		})
	}

	return resources, nil
}

func listCloudRunServices(ctx context.Context, projectID string) ([]Resource, error) {
	var resources []Resource

	client, err := run.NewServicesClient(ctx)
	if err != nil {
		return resources, retryableError(err, "create Cloud Run client")
	}
	defer client.Close()

	req := &runpb.ListServicesRequest{
		Parent: fmt.Sprintf("projects/%s/locations/-", projectID),
	}

	it := client.ListServices(ctx, req)
	for {
		service, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, retryableError(err, "list Cloud Run services")
		}

		resources = append(resources, Resource{
			Type:     "Cloud Run Service",
			Name:     extractResourceName(service.Name),
			Location: extractLocation(service.Name),
			Status:   "ACTIVE",
			Details:  "Serverless container",
		})
	}

	return resources, nil
}

func listPubSubTopics(ctx context.Context, projectID string) ([]Resource, error) {
	var resources []Resource

	client, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		return resources, retryableError(err, "create Pub/Sub client")
	}
	defer client.Close()

	it := client.Topics(ctx)
	for {
		topic, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, retryableError(err, "list Pub/Sub topics")
		}

		resources = append(resources, Resource{
			Type:     "Pub/Sub Topic",
			Name:     topic.ID(),
			Location: "global",
			Status:   "ACTIVE",
			Details:  "Message queue",
		})
	}

	return resources, nil
}

func listLoadBalancers(ctx context.Context, projectID string) ([]Resource, error) {
	var resources []Resource

	client, err := compute.NewForwardingRulesRESTClient(ctx)
	if err != nil {
		return resources, retryableError(err, "create forwarding rules client")
	}
	defer client.Close()

	req := &computepb.AggregatedListForwardingRulesRequest{
		Project: projectID,
	}

	it := client.AggregatedList(ctx, req)
	for {
		pair, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, retryableError(err, "list load balancers")
		}

		for _, rule := range pair.Value.ForwardingRules {
			lbType := "HTTP(S)"
			if rule.LoadBalancingScheme != nil {
				lbType = *rule.LoadBalancingScheme
			}

			resources = append(resources, Resource{
				Type:     "Load Balancer",
				Name:     getStringValue(rule.Name),
				Location: pair.Key,
				Status:   "ACTIVE",
				Details:  fmt.Sprintf("Type: %s", lbType),
			})
		}
	}

	return resources, nil
}

func listVPNGateways(ctx context.Context, projectID string) ([]Resource, error) {
	var resources []Resource

	client, err := compute.NewVpnGatewaysRESTClient(ctx)
	if err != nil {
		return resources, retryableError(err, "create VPN gateways client")
	}
	defer client.Close()

	req := &computepb.AggregatedListVpnGatewaysRequest{
		Project: projectID,
	}

	it := client.AggregatedList(ctx, req)
	for {
		pair, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, retryableError(err, "list VPN gateways")
		}

		for _, gateway := range pair.Value.VpnGateways {
			resources = append(resources, Resource{
				Type:     "VPN Gateway",
				Name:     getStringValue(gateway.Name),
				Location: pair.Key,
				Status:   "ACTIVE",
				Details:  "High-availability VPN",
			})
		}
	}

	return resources, nil
}

func listCloudNAT(ctx context.Context, projectID string) ([]Resource, error) {
	var resources []Resource

	client, err := compute.NewRoutersRESTClient(ctx)
	if err != nil {
		return resources, retryableError(err, "create routers client")
	}
	defer client.Close()

	req := &computepb.AggregatedListRoutersRequest{
		Project: projectID,
	}

	it := client.AggregatedList(ctx, req)
	for {
		pair, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, retryableError(err, "list Cloud NAT")
		}

		for _, router := range pair.Value.Routers {
			if len(router.Nats) > 0 {
				for _, nat := range router.Nats {
					resources = append(resources, Resource{
						Type:     "Cloud NAT",
						Name:     getStringValue(nat.Name),
						Location: pair.Key,
						Status:   "ACTIVE",
						Details:  fmt.Sprintf("Router: %s", getStringValue(router.Name)),
					})
				}
			}
		}
	}

	return resources, nil
}

func listMemorystore(ctx context.Context, projectID string) ([]Resource, error) {
	var resources []Resource

	client, err := redis.NewCloudRedisClient(ctx)
	if err != nil {
		return resources, retryableError(err, "create Memorystore client")
	}
	defer client.Close()

	req := &redispb.ListInstancesRequest{
		Parent: fmt.Sprintf("projects/%s/locations/-", projectID),
	}

	it := client.ListInstances(ctx, req)
	for {
		instance, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, retryableError(err, "list Memorystore instances")
		}

		resources = append(resources, Resource{
			Type:     "Memorystore Redis",
			Name:     extractResourceName(instance.Name),
			Location: extractLocation(instance.Name),
			Status:   instance.State.String(),
			Details:  fmt.Sprintf("Memory: %d GB, Tier: %s", instance.MemorySizeGb, instance.Tier),
		})
	}

	return resources, nil
}

func listCloudDNS(ctx context.Context, projectID string) ([]Resource, error) {
	var resources []Resource

	service, err := dns.NewService(ctx)
	if err != nil {
		return resources, retryableError(err, "create DNS client")
	}

	resp, err := service.ManagedZones.List(projectID).Do()
	if err != nil {
		return resources, retryableError(err, "list DNS zones")
	}

	for _, zone := range resp.ManagedZones {
		resources = append(resources, Resource{
			Type:     "Cloud DNS Zone",
			Name:     zone.Name,
			Location: "global",
			Status:   "ACTIVE",
			Details:  fmt.Sprintf("Domain: %s", zone.DnsName),
		})
	}

	return resources, nil
}

func listReservedIPs(ctx context.Context, projectID string) ([]Resource, error) {
	var resources []Resource

	client, err := compute.NewAddressesRESTClient(ctx)
	if err != nil {
		return resources, retryableError(err, "create addresses client")
	}
	defer client.Close()

	req := &computepb.AggregatedListAddressesRequest{
		Project: projectID,
	}

	it := client.AggregatedList(ctx, req)
	for {
		pair, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, retryableError(err, "list reserved IPs")
		}

		for _, addr := range pair.Value.Addresses {
			status := "RESERVED"
			if addr.Status != nil {
				status = *addr.Status
			}

			ipAddr := "N/A"
			if addr.Address != nil {
				ipAddr = *addr.Address
			}

			resources = append(resources, Resource{
				Type:     "Reserved IP",
				Name:     getStringValue(addr.Name),
				Location: pair.Key,
				Status:   status,
				Details:  fmt.Sprintf("IP: %s", ipAddr),
			})
		}
	}

	return resources, nil
}

func listCloudBuildTriggers(ctx context.Context, projectID string) ([]Resource, error) {
	var resources []Resource

	client, err := cloudbuild.NewClient(ctx)
	if err != nil {
		return resources, retryableError(err, "create Cloud Build client")
	}
	defer client.Close()

	// Use global location for Cloud Build triggers
	req := &cloudbuildpb.ListBuildTriggersRequest{
		Parent: fmt.Sprintf("projects/%s/locations/global", projectID),
	}

	it := client.ListBuildTriggers(ctx, req)
	for {
		trigger, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, retryableError(err, "list Cloud Build triggers")
		}

		disabled := "ENABLED"
		if trigger.Disabled {
			disabled = "DISABLED"
		}

		resources = append(resources, Resource{
			Type:     "Cloud Build Trigger",
			Name:     trigger.Name,
			Location: extractLocation(trigger.Name),
			Status:   disabled,
			Details:  fmt.Sprintf("Trigger: %s", trigger.Description),
		})
	}

	return resources, nil
}

func listCloudBuildRuns(ctx context.Context, projectID string) ([]Resource, error) {
	var resources []Resource

	client, err := cloudbuild.NewClient(ctx)
	if err != nil {
		return resources, retryableError(err, "create Cloud Build client")
	}
	defer client.Close()

	// List recent builds (last 30 days)
	thirtyDaysAgo := time.Now().AddDate(0, 0, -30)

	req := &cloudbuildpb.ListBuildsRequest{
		Parent:   fmt.Sprintf("projects/%s/locations/global", projectID),
		Filter:   fmt.Sprintf("create_time>\"%s\"", thirtyDaysAgo.Format(time.RFC3339)),
		PageSize: 100,
	}

	it := client.ListBuilds(ctx, req)
	for {
		build, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, retryableError(err, "list Cloud Build runs")
		}

		status := "UNKNOWN"
		if build.Status != 0 {
			status = build.Status.String()
		}

		// Calculate build duration if available
		var duration string
		if build.StartTime != nil && build.FinishTime != nil {
			start := build.StartTime.AsTime()
			finish := build.FinishTime.AsTime()
			duration = fmt.Sprintf("Duration: %s", finish.Sub(start).Round(time.Second))
		} else {
			duration = "In progress"
		}

		resources = append(resources, Resource{
			Type:     "Cloud Build Run",
			Name:     build.Id,
			Location: extractLocation(build.Name),
			Status:   status,
			Details:  duration,
		})
	}

	return resources, nil
}

func listArtifactRegistryRepos(ctx context.Context, projectID string) ([]Resource, error) {
	var resources []Resource

	client, err := artifactregistry.NewClient(ctx)
	if err != nil {
		return resources, retryableError(err, "create Artifact Registry client")
	}
	defer client.Close()

	// Artifact Registry doesn't support wildcard locations, so query common regions
	locations := []string{
		"us-central1", "us-east1", "us-west1", "us-west2", "us-west3", "us-west4",
		"europe-west1", "europe-west2", "europe-west3", "europe-west4",
		"asia-east1", "asia-northeast1", "asia-southeast1",
	}

	for _, location := range locations {
		req := &artifactregistrypb.ListRepositoriesRequest{
			Parent: fmt.Sprintf("projects/%s/locations/%s", projectID, location),
		}

		it := client.ListRepositories(ctx, req)
		for {
			repo, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				// Skip regions that don't have any repositories or have errors
				break
			}

			resources = append(resources, Resource{
				Type:     "Artifact Registry",
				Name:     extractResourceName(repo.Name),
				Location: extractLocation(repo.Name),
				Status:   "ACTIVE",
				Details:  fmt.Sprintf("Format: %s", repo.Format),
			})
		}
	}

	return resources, nil
}

func listVertexAIModels(ctx context.Context, projectID string) ([]Resource, error) {
	var resources []Resource

	client, err := aiplatform.NewModelClient(ctx)
	if err != nil {
		return resources, retryableError(err, "create Vertex AI Model client")
	}
	defer client.Close()

	// Vertex AI requires 'global' location, not wildcard
	req := &aiplatformpb.ListModelsRequest{
		Parent: fmt.Sprintf("projects/%s/locations/global", projectID),
	}

	it := client.ListModels(ctx, req)
	for {
		model, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, retryableError(err, "list Vertex AI models")
		}

		resources = append(resources, Resource{
			Type:     "Vertex AI Model",
			Name:     extractResourceName(model.Name),
			Location: "global",
			Status:   "ACTIVE",
			Details:  fmt.Sprintf("Display: %s", model.DisplayName),
		})
	}

	return resources, nil
}

func listVertexAIEndpoints(ctx context.Context, projectID string) ([]Resource, error) {
	var resources []Resource

	client, err := aiplatform.NewEndpointClient(ctx)
	if err != nil {
		return resources, retryableError(err, "create Vertex AI Endpoint client")
	}
	defer client.Close()

	// Vertex AI requires 'global' location, not wildcard
	req := &aiplatformpb.ListEndpointsRequest{
		Parent: fmt.Sprintf("projects/%s/locations/global", projectID),
	}

	it := client.ListEndpoints(ctx, req)
	for {
		endpoint, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, retryableError(err, "list Vertex AI endpoints")
		}

		resources = append(resources, Resource{
			Type:     "Vertex AI Endpoint",
			Name:     extractResourceName(endpoint.Name),
			Location: "global",
			Status:   "ACTIVE",
			Details:  fmt.Sprintf("Display: %s", endpoint.DisplayName),
		})
	}

	return resources, nil
}

func listVertexAICustomJobs(ctx context.Context, projectID string) ([]Resource, error) {
	var resources []Resource

	client, err := aiplatform.NewJobClient(ctx)
	if err != nil {
		return resources, retryableError(err, "create Vertex AI Job client")
	}
	defer client.Close()

	// Vertex AI custom jobs require 'global' location
	req := &aiplatformpb.ListCustomJobsRequest{
		Parent: fmt.Sprintf("projects/%s/locations/global", projectID),
	}

	it := client.ListCustomJobs(ctx, req)
	for {
		job, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, retryableError(err, "list Vertex AI custom jobs")
		}

		status := "UNKNOWN"
		if job.State != 0 {
			status = job.State.String()
		}

		resources = append(resources, Resource{
			Type:     "Vertex AI Custom Job",
			Name:     extractResourceName(job.Name),
			Location: "global",
			Status:   status,
			Details:  fmt.Sprintf("Display: %s", job.DisplayName),
		})
	}

	return resources, nil
}

// Estimate costs using BigQuery billing export
func estimateCosts(ctx context.Context, projectID string, resources []Resource) ([]Resource, float64) {
	costs, totalCost := fetchBillingData(ctx, projectID)

	// Track which cost keys have been matched to resources
	matchedCosts := make(map[string]bool)

	// Map costs to resources
	matched := 0
	for i := range resources {
		key := getResourceKey(resources[i])
		if cost, ok := costs[key]; ok {
			resources[i].MonthlyCost = cost.Amount
			resources[i].CostCurrency = cost.Currency
			matchedCosts[key] = true
			matched++
		}
	}

	if len(costs) > 0 {
		log.Printf("  Debug: Matched %d out of %d resources to billing data", matched, len(resources))
		// Show first few resource keys for debugging
		log.Printf("  Debug: Sample resource keys:")
		for i := 0; i < len(resources) && i < 5; i++ {
			key := getResourceKey(resources[i])
			if cost, ok := costs[key]; ok {
				log.Printf("    ✓ %s -> $%.2f", key, cost.Amount)
			} else {
				log.Printf("    ✗ %s -> no match", key)
			}
		}

		// Add virtual resources for unmatched costs (usage-based services)
		log.Printf("  Debug: Checking for unmatched costs (usage-based services)...")
		for key, cost := range costs {
			if !matchedCosts[key] {
				// Parse service/location from key
				parts := strings.Split(key, "/")
				if len(parts) == 2 {
					serviceName := parts[0]
					location := parts[1]

					log.Printf("    ✓ Adding usage-based service: %s (%s) = $%.2f", serviceName, location, cost.Amount)

					// Add as a virtual resource
					resources = append(resources, Resource{
						Type:         serviceName + " API Usage",
						Name:         "API Calls",
						Location:     location,
						Status:       "ACTIVE",
						Details:      "Usage-based service (last 90 days)",
						MonthlyCost:  cost.Amount,
						CostCurrency: cost.Currency,
					})
				}
			}
		}
	}

	return resources, totalCost
}

type CostData struct {
	Amount   float64
	Currency string
	SKU      string
}

func getResourceKey(r Resource) string {
	// Create a key matching the BigQuery billing data (service type and location)
	// Map resource types to billing service descriptions
	serviceMap := map[string]string{
		"Compute Engine VM":      "Compute Engine",
		"Persistent Disk":        "Compute Engine",
		"Cloud Storage Bucket":   "Cloud Storage",
		"Cloud SQL Instance":     "Cloud SQL",
		"GKE Cluster":            "Kubernetes Engine",
		"BigQuery Dataset":       "BigQuery",
		"Cloud Function":         "Cloud Functions",
		"Cloud Run Service":      "Cloud Run",
		"Pub/Sub Topic":          "Cloud Pub/Sub",
		"Load Balancer":          "Compute Engine",
		"VPN Gateway":            "Compute Engine",
		"Cloud NAT":              "Compute Engine",
		"Memorystore Redis":      "Cloud Memorystore for Redis",
		"Cloud DNS Zone":         "Cloud DNS",
		"Reserved IP":            "Compute Engine",
		"Cloud Build Trigger":    "Cloud Build",
		"Cloud Build Run":        "Cloud Build",
		"Artifact Registry":      "Artifact Registry",
		"Vertex AI Model":        "Vertex AI",
		"Vertex AI Endpoint":     "Vertex AI",
		"Vertex AI Custom Job":   "Vertex AI",
	}

	serviceType := r.Type
	if mapped, ok := serviceMap[r.Type]; ok {
		serviceType = mapped
	}

	return fmt.Sprintf("%s/%s", serviceType, r.Location)
}

func discoverBillingTables(ctx context.Context, client *bigquery.Client, projectID string) []string {
	var billingTables []string

	// List all datasets in the project
	it := client.Datasets(ctx)
	for {
		dataset, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Printf("Error listing datasets: %v", err)
			continue
		}

		// List tables in this dataset
		tables := dataset.Tables(ctx)
		for {
			table, err := tables.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				log.Printf("Error listing tables in dataset %s: %v", dataset.DatasetID, err)
				continue
			}

			// Check if table name matches billing export patterns
			tableName := table.TableID
			if strings.HasPrefix(tableName, "gcp_billing_export_v1_") ||
				strings.HasPrefix(tableName, "gcp_billing_export_resource_v1_") {
				fullTableName := fmt.Sprintf("%s.%s.%s", projectID, dataset.DatasetID, tableName)
				log.Printf("  ✓ Found billing export table: %s", fullTableName)
				billingTables = append(billingTables, fullTableName)
			}
		}
	}

	return billingTables
}

func fetchBillingData(ctx context.Context, projectID string) (map[string]CostData, float64) {
	costs := make(map[string]CostData)

	// Try to fetch billing data from BigQuery export
	client, err := bigquery.NewClient(ctx, projectID)
	if err != nil {
		log.Printf("Cannot create BigQuery client: %v", err)
		return costs, 0.0
	}
	defer client.Close()

	// Get current month date range
	now := time.Now()
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	// Discover billing export tables by listing all datasets and tables
	billingTables := discoverBillingTables(ctx, client, projectID)

	if len(billingTables) == 0 {
		log.Printf("No billing export tables found. Please enable BigQuery billing export.")
		return costs, 0.0
	}

	var totalCost float64
	var lastError error

	for _, tableName := range billingTables {
		log.Printf("Trying BigQuery table: %s", tableName)

		// First, check if table has any data at all and show date range
		dateRangeQuery := client.Query(fmt.Sprintf(`
			SELECT
				COUNT(*) as total,
				MIN(DATE(usage_start_time)) as min_date,
				MAX(DATE(usage_start_time)) as max_date
			FROM `+"`%s`", tableName))
		dateIt, err := dateRangeQuery.Read(ctx)
		if err == nil {
			var dateRow struct {
				Total   int64     `bigquery:"total"`
				MinDate time.Time `bigquery:"min_date"`
				MaxDate time.Time `bigquery:"max_date"`
			}
			if err := dateIt.Next(&dateRow); err == nil {
				log.Printf("  Debug: Table has %d rows from %s to %s", dateRow.Total, dateRow.MinDate.Format("2006-01-02"), dateRow.MaxDate.Format("2006-01-02"))
				log.Printf("  Debug: Querying for data since %s", startOfMonth.Format("2006-01-02"))
			}
		}

		// Query for last 90 days to ensure we capture all recent billing data
		// (current month might not have data yet, previous month should be complete)
		ninetyDaysAgo := now.AddDate(0, 0, -90)
		query := client.Query(fmt.Sprintf(`
			SELECT
				service.description as service_type,
				location.location as location,
				SUM(cost) as cost,
				currency
			FROM `+"`%s`"+`
			WHERE DATE(usage_start_time) >= DATE('%s')
				AND cost > 0
			GROUP BY service_type, location, currency
		`, tableName, ninetyDaysAgo.Format("2006-01-02")))

		it, err := query.Read(ctx)
		if err != nil {
			log.Printf("  ✗ Error querying table %s: %v", tableName, err)
			lastError = err
			continue // Try next table
		}

		rowCount := 0
		for {
			var row struct {
				ServiceType string  `bigquery:"service_type"`
				Location    string  `bigquery:"location"`
				Cost        float64 `bigquery:"cost"`
				Currency    string  `bigquery:"currency"`
			}

			err := it.Next(&row)
			if err == iterator.Done {
				break
			}
			if err != nil {
				log.Printf("  ✗ Error reading row: %v", err)
				lastError = err
				continue
			}

			rowCount++
			// Group costs by service type and location
			key := fmt.Sprintf("%s/%s", row.ServiceType, row.Location)

			// Debug: show first few cost entries
			if rowCount <= 10 {
				log.Printf("  Debug: Cost entry #%d: %s / %s = $%.2f", rowCount, row.ServiceType, row.Location, row.Cost)
			}

			if existing, ok := costs[key]; ok {
				// Aggregate costs for the same service/location
				costs[key] = CostData{
					Amount:   existing.Amount + row.Cost,
					Currency: row.Currency,
					SKU:      existing.SKU,
				}
			} else {
				costs[key] = CostData{
					Amount:   row.Cost,
					Currency: row.Currency,
					SKU:      row.ServiceType,
				}
			}
			totalCost += row.Cost
		}

		if totalCost > 0 {
			log.Printf("  ✓ Found %d billing records from last 90 days, total cost: $%.2f", rowCount, totalCost)
			// Show all unique cost keys for debugging
			log.Printf("  Debug: Available cost keys:")
			count := 0
			for key := range costs {
				count++
				if count <= 10 {
					log.Printf("    - %s", key)
				}
			}
			if count > 10 {
				log.Printf("    ... and %d more", count-10)
			}
			break // Found data, no need to try other tables
		} else {
			log.Printf("  ✗ No billing data found for last 90 days")
		}
	}

	// If BigQuery export not found, try using Cloud Billing API
	if totalCost == 0 {
		if lastError != nil {
			log.Printf("BigQuery billing export not accessible. Last error: %v", lastError)
		}
		totalCost = fetchCostsFromBillingAPI(ctx, projectID, costs)
	}

	return costs, totalCost
}

func fetchCostsFromBillingAPI(ctx context.Context, projectID string, costs map[string]CostData) float64 {
	// Get billing account for the project
	client, err := billing.NewCloudBillingClient(ctx)
	if err != nil {
		return 0.0
	}
	defer client.Close()

	req := &billingpb.GetProjectBillingInfoRequest{
		Name: fmt.Sprintf("projects/%s", projectID),
	}

	info, err := client.GetProjectBillingInfo(ctx, req)
	if err != nil {
		return 0.0
	}

	if info.BillingAccountName == "" {
		return 0.0
	}

	// Note: The Cloud Billing API doesn't provide detailed month-to-date costs
	// This would require BigQuery billing export to be enabled
	// Return 0 and costs will remain empty
	return 0.0
}

func displayResourceTable(resources []Resource, totalCost float64) {
	// Simple custom table formatter with cost column
	fmt.Printf("%-5s %-22s %-28s %-18s %-12s %-12s %s\n", "#", "Type", "Name", "Location", "MTD Cost", "Status", "Details")
	fmt.Println(strings.Repeat("-", 150))

	// Group resources by type for summary
	typeCount := make(map[string]int)
	typeCost := make(map[string]float64)
	for _, resource := range resources {
		typeCount[resource.Type]++
		typeCost[resource.Type] += resource.MonthlyCost
	}

	// Display rows
	for i, resource := range resources {
		name := resource.Name
		if len(name) > 28 {
			name = name[:25] + "..."
		}
		location := resource.Location
		if len(location) > 18 {
			location = location[:15] + "..."
		}
		status := resource.Status
		if len(status) > 12 {
			status = status[:9] + "..."
		}
		details := resource.Details
		if len(details) > 45 {
			details = details[:42] + "..."
		}

		// Format cost
		costStr := "-"
		if resource.MonthlyCost > 0 {
			if resource.CostCurrency != "" {
				costStr = fmt.Sprintf("%s %.2f", resource.CostCurrency, resource.MonthlyCost)
			} else {
				costStr = fmt.Sprintf("$%.2f", resource.MonthlyCost)
			}
		}

		fmt.Printf("%-5d %-22s %-28s %-18s %-12s %-12s %s\n",
			i+1,
			resource.Type,
			name,
			location,
			costStr,
			status,
			details,
		)
	}

	// Display summary
	fmt.Println("\n" + strings.Repeat("─", 80))
	fmt.Printf("SUMMARY: %d total resources", len(resources))
	if totalCost > 0 {
		fmt.Printf(" | Month-to-Date Cost: $%.2f", totalCost)
	}
	fmt.Println()
	fmt.Println(strings.Repeat("─", 80))

	// Show cost breakdown by resource type
	fmt.Printf("%-30s %10s %15s\n", "Resource Type", "Count", "MTD Cost")
	fmt.Println(strings.Repeat("-", 60))
	for resType, count := range typeCount {
		cost := typeCost[resType]
		costStr := "-"
		if cost > 0 {
			costStr = fmt.Sprintf("$%.2f", cost)
		}
		fmt.Printf("%-30s %10d %15s\n", resType, count, costStr)
	}

	fmt.Println(strings.Repeat("─", 80))

	if totalCost > 0 {
		fmt.Printf("💰 Total Month-to-Date Cost: $%.2f USD\n", totalCost)
		fmt.Println("\nNote: Costs are fetched from BigQuery billing export (current month)")
	} else {
		fmt.Println("Note: Enable BigQuery billing export to see month-to-date costs")
		fmt.Println("      Visit: https://console.cloud.google.com/billing/export")
	}
}

// Helper functions
func getStringValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func extractLocation(resourceName string) string {
	parts := strings.Split(resourceName, "/")
	for i, part := range parts {
		if part == "locations" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return "unknown"
}

func extractResourceName(fullName string) string {
	parts := strings.Split(fullName, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return fullName
}

func retryableError(err error, operation string) error {
	// Simple retry logic could be added here
	return fmt.Errorf("%s: %w", operation, err)
}

func isAPINotEnabledError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := strings.ToLower(err.Error())
	return strings.Contains(errMsg, "not enabled") ||
		strings.Contains(errMsg, "api not enabled") ||
		strings.Contains(errMsg, "disabled") ||
		strings.Contains(errMsg, "enable it by visiting")
}

// Cache implementation
func NewCache(filename string, expiry time.Duration) *Cache {
	return &Cache{
		cacheFile:   filename,
		cacheExpiry: expiry,
	}
}

func (c *Cache) Get(projectID string) (*Cache, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	data, err := os.ReadFile(c.cacheFile)
	if err != nil {
		return nil, false
	}

	var cached Cache
	if err := json.Unmarshal(data, &cached); err != nil {
		return nil, false
	}

	if cached.ProjectID != projectID {
		return nil, false
	}

	if time.Since(cached.Timestamp) > c.cacheExpiry {
		return nil, false
	}

	return &cached, true
}

func (c *Cache) Set(projectID string, resources []Resource, totalCost float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.ProjectID = projectID
	c.Resources = resources
	c.Timestamp = time.Now()
	c.TotalCost = totalCost

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		log.Printf("Failed to marshal cache: %v", err)
		return
	}

	if err := os.WriteFile(c.cacheFile, data, 0644); err != nil {
		log.Printf("Failed to write cache: %v", err)
	}
}
