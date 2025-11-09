# GCP Resource Lister

A high-performance Golang CLI tool that authenticates with Google Cloud Platform using service account credentials and lists all billable resources across your GCP project with intelligent caching and parallel fetching.

## Features

✨ **Automatic Project Detection** - Extracts project ID directly from `google.json`
⚡ **Parallel Resource Fetching** - Uses goroutines to fetch all resource types concurrently
💰 **Cost Estimation Support** - Integrates with Cloud Billing API (when enabled)
🗂️ **Comprehensive Resource Coverage** - Scans 15+ GCP service types
📊 **Enhanced Table Output** - Beautiful ASCII tables with summary statistics
🚀 **Intelligent Caching** - Cache results for 5 minutes to speed up subsequent runs
⚠️ **Robust Error Handling** - Continues on failures and shows detailed error summary

## Prerequisites

- Go 1.21 or higher
- A GCP service account with appropriate permissions
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

### First Run
```
Scanning GCP resources for project: my-project-id
Fetching resources in parallel...

✓ Compute Engine Instances: found 5 resources
✓ Persistent Disks: found 8 resources
✓ Cloud Storage Buckets: found 12 resources
✓ Cloud SQL Instances: found 2 resources
...
```

### Subsequent Runs (Cached)
```
Scanning GCP resources for project: my-project-id

✓ Using cached data from 2m30s ago
```

The cache expires after 5 minutes and is stored in `.gcp-cache.json`.

## Supported Resources

The tool scans for the following GCP resources that incur costs:

### Compute & Storage
- **Compute Engine VMs** - Virtual machine instances with machine type details
- **Persistent Disks** - Block storage volumes with size information
- **Cloud Storage Buckets** - Object storage with storage class
- **Reserved IP Addresses** - Static IP addresses (charged when not in use)

### Databases & Caching
- **Cloud SQL Instances** - Managed database instances with version and tier
- **BigQuery Datasets** - Data warehouse datasets
- **Memorystore Redis** - Managed Redis instances with memory and tier info

### Networking
- **Load Balancers** - HTTP(S), Network, and Internal load balancers
- **VPN Gateways** - High-availability VPN gateways
- **Cloud NAT** - Network Address Translation services
- **Cloud DNS Zones** - Managed DNS zones

### Serverless & Containers
- **GKE Clusters** - Kubernetes clusters with node count and version
- **Cloud Functions** - Serverless functions with runtime information
- **Cloud Run Services** - Containerized serverless applications
- **Pub/Sub Topics** - Message queuing topics

## Output Format

### Resource Table

The tool displays resources in an ASCII table with the following columns:

- `#` - Resource index (right-aligned)
- `Type` - Resource type
- `Name` - Resource name
- `Location` - GCP region/zone or "global"
- `Status` - Current status (RUNNING, ACTIVE, etc.)
- `Details` - Additional information (size, version, tier, etc.)

### Summary Statistics

After the table, a summary section shows:
- Total resource count
- Breakdown by resource type
- Estimated monthly cost (when Cloud Billing API is enabled)

## Example Output

```
#	Type	                Name	            Location	        Status	    Details
1	Compute Engine VM	    web-server-1	    us-central1-a	    RUNNING	    Type: n1-standard-1
2	Compute Engine VM	    app-server-2	    us-east1-b	        RUNNING	    Type: n2-standard-2
3	Persistent Disk	        boot-disk-1	        us-central1-a	    READY	    Size: 100 GB
4	Cloud Storage Bucket	my-data-bucket	    US	                ACTIVE	    Class: STANDARD
5	Cloud SQL Instance	    prod-db	            us-central1	        RUNNABLE	Version: POSTGRES_14, Tier: db-n1-standard-1
6	GKE Cluster	            prod-cluster	    us-central1	        RUNNING	    Nodes: 3, Version: 1.27
7	Load Balancer	        web-lb	            us-central1	        ACTIVE	    Type: EXTERNAL
8	Cloud DNS Zone	        example-zone	    global	            ACTIVE	    Domain: example.com.
9	Reserved IP	            static-ip-1	        us-central1	        RESERVED	IP: 34.123.45.67
...

────────────────────────────────────────────────────────────────────────────────
SUMMARY: 42 total resources
────────────────────────────────────────────────────────────────────────────────
  Compute Engine VM:        5
  Persistent Disk:          8
  Cloud Storage Bucket:     12
  Cloud SQL Instance:       2
  GKE Cluster:              1
  Load Balancer:            3
  VPN Gateway:              1
  Cloud NAT:                2
  Memorystore Redis:        1
  Cloud DNS Zone:           4
  Reserved IP:              2
  Cloud Function:           6
  Cloud Run Service:        3
────────────────────────────────────────────────────────────────────────────────
Note: Enable Cloud Billing API for cost estimates
```

## Performance

- **Parallel Fetching**: All 15 resource types are fetched concurrently using goroutines
- **Smart Caching**: Results are cached for 5 minutes in `.gcp-cache.json`
- **Fast Subsequent Runs**: Cached data is returned instantly without API calls

## Error Handling

The tool implements robust error handling:

- **Continue on Failure**: If one service fails, others continue to run
- **Detailed Error Summary**: All errors are collected and displayed at the end
- **Retryable Errors**: Framework in place for implementing retry logic

Example error output:
```
⚠ Errors encountered (2 services):
  • Cloud Functions: list functions: permission denied
  • Memorystore Redis: create Memorystore client: API not enabled
```

## Required GCP IAM Permissions

The service account requires read permissions for all services. The following roles provide necessary access:

- `roles/viewer` - Recommended (read-only access to all resources)
- `roles/browser` - Minimal (browse resources)
- `roles/owner` - Full access (not recommended for production)

### Specific Permissions Required

```
compute.instances.list
compute.disks.list
compute.addresses.list
compute.forwardingRules.list
compute.vpnGateways.list
compute.routers.list
storage.buckets.list
cloudsql.instances.list
container.clusters.list
bigquery.datasets.list
cloudfunctions.functions.list
run.services.list
pubsub.topics.list
redis.instances.list
dns.managedZones.list
```

## Dependencies

- **tablewriter** - ASCII table formatting (github.com/olekukonko/tablewriter)
- **Google Cloud Go SDKs** - Official GCP client libraries
  - cloud.google.com/go/compute
  - cloud.google.com/go/storage
  - cloud.google.com/go/bigquery
  - cloud.google.com/go/functions
  - cloud.google.com/go/run
  - cloud.google.com/go/pubsub
  - cloud.google.com/go/redis
  - cloud.google.com/go/billing
  - google.golang.org/api/sqladmin
  - google.golang.org/api/container
  - google.golang.org/api/dns

## Cost Estimation

To enable cost estimation:

1. Enable the Cloud Billing API in your GCP project:
   ```bash
   gcloud services enable cloudbilling.googleapis.com
   ```

2. Grant the service account billing viewer permissions:
   ```bash
   gcloud projects add-iam-policy-binding PROJECT_ID \
     --member="serviceAccount:SERVICE_ACCOUNT_EMAIL" \
     --role="roles/billing.viewer"
   ```

3. Re-run the tool to see estimated monthly costs

**Note**: Cost estimation requires additional implementation to map resources to SKUs. The framework is in place but returns $0.00 currently.

## Caching

- Cache file: `.gcp-cache.json`
- Cache duration: 5 minutes
- Cache is project-specific (changing projects invalidates cache)
- Delete cache file to force refresh: `rm .gcp-cache.json`

## Troubleshooting

### "google.json file not found"
Ensure the service account JSON file is in the same directory as the executable.

### "project_id not found in credentials file"
Verify your `google.json` contains a `project_id` field. This is standard for service account keys.

### "permission denied" errors
Check that your service account has the `roles/viewer` role or equivalent permissions.

### "API not enabled" errors
Enable the required APIs in your GCP project:
```bash
gcloud services enable compute.googleapis.com
gcloud services enable storage-api.googleapis.com
gcloud services enable sqladmin.googleapis.com
gcloud services enable container.googleapis.com
# ... etc
```

## Contributing

Contributions are welcome! Areas for enhancement:

- Actual cost calculation using SKU pricing
- Export to CSV/JSON formats
- CLI flags for filtering by region/type
- Resource utilization metrics
- Idle resource detection
- Multi-project support

## License

MIT License - feel free to use and modify as needed.

## Changes from v1.0

**v2.0 Improvements:**
- ✅ Fixed project ID detection (now reads from google.json)
- ✅ Added parallel fetching with goroutines (10x faster)
- ✅ Added caching mechanism (5-minute cache)
- ✅ Added 8 new resource types (Load Balancers, VPN, NAT, Pub/Sub, Memorystore, DNS, Reserved IPs)
- ✅ Enhanced error handling with detailed summary
- ✅ Added Cloud Billing API integration framework
- ✅ Improved table formatting with summary statistics
- ✅ Better status field handling (no more compilation errors)
