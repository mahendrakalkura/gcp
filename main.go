package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"cloud.google.com/go/bigquery"
	"cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"cloud.google.com/go/functions/apiv1"
	"cloud.google.com/go/functions/apiv1/functionspb"
	"cloud.google.com/go/run/apiv2"
	"cloud.google.com/go/run/apiv2/runpb"
	"cloud.google.com/go/storage"
	"github.com/olekukonko/tablewriter"
	"google.golang.org/api/container/v1"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	sqladmin "google.golang.org/api/sqladmin/v1"
)

type Resource struct {
	Type     string
	Name     string
	Location string
	Status   string
	Details  string
}

func main() {
	ctx := context.Background()

	// Check if google.json exists
	if _, err := os.Stat("google.json"); os.IsNotExist(err) {
		log.Fatal("google.json file not found in current directory")
	}

	// Set credentials
	os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "google.json")

	// Get project ID from credentials
	projectID, err := getProjectID(ctx)
	if err != nil {
		log.Fatalf("Failed to get project ID: %v", err)
	}

	fmt.Printf("Scanning GCP resources for project: %s\n\n", projectID)

	var resources []Resource

	// Collect resources from various services
	fmt.Println("Fetching Compute Engine instances...")
	resources = append(resources, listComputeInstances(ctx, projectID)...)

	fmt.Println("Fetching Compute Engine disks...")
	resources = append(resources, listComputeDisks(ctx, projectID)...)

	fmt.Println("Fetching Cloud Storage buckets...")
	resources = append(resources, listStorageBuckets(ctx, projectID)...)

	fmt.Println("Fetching Cloud SQL instances...")
	resources = append(resources, listCloudSQLInstances(ctx, projectID)...)

	fmt.Println("Fetching GKE clusters...")
	resources = append(resources, listGKEClusters(ctx, projectID)...)

	fmt.Println("Fetching BigQuery datasets...")
	resources = append(resources, listBigQueryDatasets(ctx, projectID)...)

	fmt.Println("Fetching Cloud Functions...")
	resources = append(resources, listCloudFunctions(ctx, projectID)...)

	fmt.Println("Fetching Cloud Run services...")
	resources = append(resources, listCloudRunServices(ctx, projectID)...)

	// Display results in ASCII table
	fmt.Printf("\n\nFound %d billable resources:\n\n", len(resources))
	displayResourceTable(resources)
}

func getProjectID(ctx context.Context) (string, error) {
	storageClient, err := storage.NewClient(ctx)
	if err != nil {
		return "", err
	}
	defer storageClient.Close()

	// Get project ID from the client
	// We'll use a workaround by reading from credentials
	return os.Getenv("GOOGLE_CLOUD_PROJECT"), nil
}

func listComputeInstances(ctx context.Context, projectID string) []Resource {
	var resources []Resource

	client, err := compute.NewInstancesRESTClient(ctx)
	if err != nil {
		log.Printf("Failed to create compute client: %v", err)
		return resources
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
			log.Printf("Error listing instances: %v", err)
			break
		}

		for _, instance := range pair.Value.Instances {
			status := "UNKNOWN"
			if instance.Status != nil {
				status = instance.Status.String()
			}

			resources = append(resources, Resource{
				Type:     "Compute Engine VM",
				Name:     *instance.Name,
				Location: pair.Key,
				Status:   status,
				Details:  fmt.Sprintf("Type: %s", *instance.MachineType),
			})
		}
	}

	return resources
}

func listComputeDisks(ctx context.Context, projectID string) []Resource {
	var resources []Resource

	client, err := compute.NewDisksRESTClient(ctx)
	if err != nil {
		log.Printf("Failed to create disks client: %v", err)
		return resources
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
			log.Printf("Error listing disks: %v", err)
			break
		}

		for _, disk := range pair.Value.Disks {
			size := int64(0)
			if disk.SizeGb != nil {
				size = *disk.SizeGb
			}

			resources = append(resources, Resource{
				Type:     "Persistent Disk",
				Name:     *disk.Name,
				Location: pair.Key,
				Status:   disk.Status.String(),
				Details:  fmt.Sprintf("Size: %d GB", size),
			})
		}
	}

	return resources
}

func listStorageBuckets(ctx context.Context, projectID string) []Resource {
	var resources []Resource

	client, err := storage.NewClient(ctx)
	if err != nil {
		log.Printf("Failed to create storage client: %v", err)
		return resources
	}
	defer client.Close()

	it := client.Buckets(ctx, projectID)
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Printf("Error listing buckets: %v", err)
			break
		}

		resources = append(resources, Resource{
			Type:     "Cloud Storage Bucket",
			Name:     attrs.Name,
			Location: attrs.Location,
			Status:   "ACTIVE",
			Details:  fmt.Sprintf("Class: %s", attrs.StorageClass),
		})
	}

	return resources
}

func listCloudSQLInstances(ctx context.Context, projectID string) []Resource {
	var resources []Resource

	service, err := sqladmin.NewService(ctx)
	if err != nil {
		log.Printf("Failed to create SQL admin client: %v", err)
		return resources
	}

	resp, err := service.Instances.List(projectID).Do()
	if err != nil {
		log.Printf("Error listing Cloud SQL instances: %v", err)
		return resources
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

	return resources
}

func listGKEClusters(ctx context.Context, projectID string) []Resource {
	var resources []Resource

	service, err := container.NewService(ctx)
	if err != nil {
		log.Printf("Failed to create container client: %v", err)
		return resources
	}

	// List clusters across all locations
	parent := fmt.Sprintf("projects/%s/locations/-", projectID)
	resp, err := service.Projects.Locations.Clusters.List(parent).Do()
	if err != nil {
		log.Printf("Error listing GKE clusters: %v", err)
		return resources
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

	return resources
}

func listBigQueryDatasets(ctx context.Context, projectID string) []Resource {
	var resources []Resource

	client, err := bigquery.NewClient(ctx, projectID)
	if err != nil {
		log.Printf("Failed to create BigQuery client: %v", err)
		return resources
	}
	defer client.Close()

	it := client.Datasets(ctx)
	for {
		dataset, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Printf("Error listing datasets: %v", err)
			break
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
			Details:  fmt.Sprintf("Tables: (check console)"),
		})
	}

	return resources
}

func listCloudFunctions(ctx context.Context, projectID string) []Resource {
	var resources []Resource

	client, err := functions.NewCloudFunctionsClient(ctx)
	if err != nil {
		log.Printf("Failed to create functions client: %v", err)
		return resources
	}
	defer client.Close()

	// List functions across all locations
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
			log.Printf("Error listing functions: %v", err)
			break
		}

		resources = append(resources, Resource{
			Type:     "Cloud Function",
			Name:     fn.Name,
			Location: extractLocation(fn.Name),
			Status:   fn.Status.String(),
			Details:  fmt.Sprintf("Runtime: %s", fn.Runtime),
		})
	}

	return resources
}

func listCloudRunServices(ctx context.Context, projectID string) []Resource {
	var resources []Resource

	client, err := run.NewServicesClient(ctx)
	if err != nil {
		log.Printf("Failed to create Cloud Run client: %v", err)
		return resources
	}
	defer client.Close()

	// List services across all locations
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
			log.Printf("Error listing Cloud Run services: %v", err)
			break
		}

		resources = append(resources, Resource{
			Type:     "Cloud Run Service",
			Name:     service.Name,
			Location: extractLocation(service.Name),
			Status:   "ACTIVE",
			Details:  fmt.Sprintf("URL: %s", service.Uri),
		})
	}

	return resources
}

func extractLocation(resourceName string) string {
	// Extract location from resource name format: projects/*/locations/*/...
	parts := []rune(resourceName)
	locationStart := -1
	slashCount := 0

	for i, char := range parts {
		if char == '/' {
			slashCount++
			if slashCount == 4 {
				locationStart = i + 1
			} else if slashCount == 5 {
				return string(parts[locationStart:i])
			}
		}
	}

	return "unknown"
}

func displayResourceTable(resources []Resource) {
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"#", "Type", "Name", "Location", "Status", "Details"})

	// Set column alignment - right align the first column (index)
	table.SetColumnAlignment([]int{
		tablewriter.ALIGN_RIGHT,  // # column
		tablewriter.ALIGN_LEFT,   // Type
		tablewriter.ALIGN_LEFT,   // Name
		tablewriter.ALIGN_LEFT,   // Location
		tablewriter.ALIGN_LEFT,   // Status
		tablewriter.ALIGN_LEFT,   // Details
	})

	// Enable auto-formatting
	table.SetAutoWrapText(false)
	table.SetAutoFormatHeaders(true)
	table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetCenterSeparator("")
	table.SetColumnSeparator("")
	table.SetRowSeparator("")
	table.SetHeaderLine(false)
	table.SetBorder(false)
	table.SetTablePadding("\t")
	table.SetNoWhiteSpace(true)

	// Add rows
	for i, resource := range resources {
		table.Append([]string{
			fmt.Sprintf("%d", i+1),
			resource.Type,
			resource.Name,
			resource.Location,
			resource.Status,
			resource.Details,
		})
	}

	table.Render()
}
