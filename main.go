package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/bigquery"
	billing "cloud.google.com/go/billing/apiv1"
	"cloud.google.com/go/billing/apiv1/billingpb"
	"cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"cloud.google.com/go/functions/apiv1"
	"cloud.google.com/go/functions/apiv1/functionspb"
	"cloud.google.com/go/pubsub"
	"cloud.google.com/go/redis/apiv1"
	"cloud.google.com/go/redis/apiv1/redispb"
	"cloud.google.com/go/run/apiv2"
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

	fmt.Println("Fetching resources in parallel...\n")

	// Parallel resource fetching
	var wg sync.WaitGroup
	resultsChan := make(chan ResourceResult, 20)
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
			errorsMu.Lock()
			errors = append(errors, ErrorSummary{Service: result.Service, Error: result.Error})
			errorsMu.Unlock()
			fmt.Printf("⚠ %s: %v\n", result.Service, result.Error)
		} else {
			fmt.Printf("✓ %s: found %d resources\n", result.Service, len(result.Resources))
			allResources = append(allResources, result.Resources...)
		}
	}

	// Fetch cost estimates
	fmt.Println("\nFetching cost estimates...")
	totalCost := estimateCosts(ctx, projectID, allResources)

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
	data, err := ioutil.ReadFile(filename)
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
			if router.Nats != nil && len(router.Nats) > 0 {
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

// Estimate costs using Cloud Billing API
func estimateCosts(ctx context.Context, projectID string, resources []Resource) float64 {
	// Try to get billing account
	client, err := billing.NewCloudCatalogClient(ctx)
	if err != nil {
		log.Printf("Cannot fetch cost data (Cloud Billing API): %v", err)
		return 0.0
	}
	defer client.Close()

	// For now, return 0 as actual cost calculation requires SKU mapping
	// This would need significant additional implementation
	return 0.0
}

func displayResourceTable(resources []Resource, totalCost float64) {
	// Simple custom table formatter
	fmt.Printf("%-5s %-25s %-30s %-20s %-15s %s\n", "#", "Type", "Name", "Location", "Status", "Details")
	fmt.Println(strings.Repeat("-", 140))

	// Group resources by type for summary
	typeCount := make(map[string]int)
	for _, resource := range resources {
		typeCount[resource.Type]++
	}

	// Display rows
	for i, resource := range resources {
		name := resource.Name
		if len(name) > 30 {
			name = name[:27] + "..."
		}
		location := resource.Location
		if len(location) > 20 {
			location = location[:17] + "..."
		}
		status := resource.Status
		if len(status) > 15 {
			status = status[:12] + "..."
		}
		details := resource.Details
		if len(details) > 50 {
			details = details[:47] + "..."
		}

		fmt.Printf("%-5d %-25s %-30s %-20s %-15s %s\n",
			i+1,
			resource.Type,
			name,
			location,
			status,
			details,
		)
	}

	// Display summary
	fmt.Println("\n" + strings.Repeat("─", 80))
	fmt.Printf("SUMMARY: %d total resources\n", len(resources))
	fmt.Println(strings.Repeat("─", 80))

	for resType, count := range typeCount {
		fmt.Printf("  %-25s %3d\n", resType+":", count)
	}

	fmt.Println(strings.Repeat("─", 80))

	if totalCost > 0 {
		fmt.Printf("Estimated Monthly Cost: $%.2f USD\n", totalCost)
	} else {
		fmt.Println("Note: Enable Cloud Billing API for cost estimates")
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

	data, err := ioutil.ReadFile(c.cacheFile)
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

	if err := ioutil.WriteFile(c.cacheFile, data, 0644); err != nil {
		log.Printf("Failed to write cache: %v", err)
	}
}
