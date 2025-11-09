# GCP Resource Lister

A Golang CLI tool that authenticates with Google Cloud Platform using service account credentials and lists all billable resources in an ASCII table format.

## Prerequisites

- Go 1.21 or higher
- A GCP service account with `owner` permissions
- Service account JSON key file named `google.json`

## Installation

1. Clone this repository
2. Place your `google.json` service account credentials file in the same directory as `main.go`
3. Install dependencies:
   ```bash
   go mod download
   ```

## Building

Build the executable:
```bash
go build -o main .
```

## Usage

Simply run the executable:
```bash
./main
```

The program will:
1. Authenticate using the `google.json` credentials
2. Scan your GCP project for billable resources across multiple services
3. Display results in a formatted ASCII table

## Supported Resources

The tool scans for the following GCP resources that incur costs:

- **Compute Engine VMs** - Virtual machine instances
- **Persistent Disks** - Block storage volumes
- **Cloud Storage Buckets** - Object storage
- **Cloud SQL Instances** - Managed database instances
- **GKE Clusters** - Kubernetes clusters with node information
- **BigQuery Datasets** - Data warehouse datasets
- **Cloud Functions** - Serverless functions
- **Cloud Run Services** - Containerized serverless applications

## Output Format

The tool displays resources in an ASCII table with the following columns:

- `#` - Resource index (right-aligned)
- `Type` - Resource type
- `Name` - Resource name
- `Location` - GCP region/zone
- `Status` - Current status
- `Details` - Additional information (size, version, tier, etc.)

## Example Output

```
Scanning GCP resources for project: my-gcp-project

Fetching Compute Engine instances...
Fetching Compute Engine disks...
Fetching Cloud Storage buckets...
Fetching Cloud SQL instances...
Fetching GKE clusters...
Fetching BigQuery datasets...
Fetching Cloud Functions...
Fetching Cloud Run services...


Found 15 billable resources:

#	Type	                Name	            Location	        Status	    Details
1	Compute Engine VM	    web-server-1	    us-central1-a	    RUNNING	    Type: n1-standard-1
2	Persistent Disk	        disk-1	            us-central1-a	    READY	    Size: 100 GB
3	Cloud Storage Bucket	my-bucket	        US	                ACTIVE	    Class: STANDARD
4	Cloud SQL Instance	    db-prod	            us-central1	        RUNNABLE	Version: POSTGRES_14, Tier: db-n1-standard-1
5	GKE Cluster	            prod-cluster	    us-central1	        RUNNING	    Nodes: 3, Version: 1.27
```

## Required GCP IAM Permissions

The service account requires the following permissions to list resources:

- `compute.instances.list`
- `compute.disks.list`
- `storage.buckets.list`
- `cloudsql.instances.list`
- `container.clusters.list`
- `bigquery.datasets.list`
- `cloudfunctions.functions.list`
- `run.services.list`

These are typically included in the following roles:
- `roles/owner`
- `roles/viewer`
- `roles/browser`

## Dependencies

- **tablewriter** - ASCII table formatting (github.com/olekukonko/tablewriter)
- **Google Cloud Go SDKs** - Official GCP client libraries
  - cloud.google.com/go/compute
  - cloud.google.com/go/storage
  - cloud.google.com/go/bigquery
  - cloud.google.com/go/functions
  - cloud.google.com/go/run
  - google.golang.org/api/sqladmin
  - google.golang.org/api/container

## Error Handling

If the `google.json` file is not found, the program will exit with an error message. Make sure the file exists in the same directory as the executable.

If API calls fail (due to permissions or network issues), the program will log errors but continue scanning other resource types.

## Notes

- The tool queries resources across all regions/zones using aggregated list operations where available
- Some resource details may require additional API calls and are simplified in the current version
- The ASCII table uses right-aligned formatting for the index column and left-aligned for other columns
- Progress messages are displayed while fetching resources from each service
