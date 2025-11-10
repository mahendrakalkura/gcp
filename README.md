# GCP Resource Lister

A high-performance Golang CLI tool that authenticates with Google Cloud Platform using service account credentials and lists all billable resources across your GCP project with intelligent caching and parallel fetching.

## Features

✨ **Automatic Project Detection** - Extracts project ID directly from `google.json`
⚡ **Parallel Resource Fetching** - Uses goroutines to fetch all resource types concurrently
💰 **Month-to-Date Costs** - Shows actual MTD costs per resource from BigQuery billing export (last 90 days)
🗂️ **Comprehensive Resource Coverage** - Scans 21+ GCP service types including usage-based APIs
📊 **Enhanced Table Output** - Beautiful ASCII tables with cost breakdown and summary statistics
🚀 **Intelligent Caching** - Cache results for 5 minutes to speed up subsequent runs
⚠️ **Robust Error Handling** - Continues on failures and shows detailed error summary
🔍 **Usage-Based API Detection** - Automatically detects and displays costs from usage-based APIs (Text-to-Speech, Gemini, etc.)
🏷️ **Label Filtering & Grouping** - Filter resources by labels and group costs by label values (team, environment, project, etc.)

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

### Filter by Labels

Filter resources by labels to focus on specific teams, environments, or projects:

```bash
# Show only resources with a specific label key
./main --label=environment

# Show only resources with a specific label key and value
./main --label=environment:production

# Show only resources tagged for a specific team
./main --label=team:backend
```

### Group Costs by Labels

View cost breakdown by label values to understand spending by team, environment, or any custom label:

```bash
# Group costs by environment label
./main --group-by-label=environment

# Group costs by team label
./main --group-by-label=team

# Group costs by project label
./main --group-by-label=project
```

This will display the regular resource table followed by a cost summary grouped by the specified label:

```
────────────────────────────────────────────────────────────────────────────────
Cost Breakdown by Label: environment
────────────────────────────────────────────────────────────────────────────────
Label Value                        Count     Total Cost
------------------------------------------------------------
production                            12        $1,245.50
staging                                8          $432.80
development                            5          $162.23
(no label)                             3           $45.00
────────────────────────────────────────────────────────────────────────────────
```

**Supported Resources with Labels:**
- Compute Engine VMs
- Persistent Disks
- Cloud Storage Buckets

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

### Development & AI/ML
- **Cloud Build Triggers** - CI/CD build triggers (enabled/disabled status)
- **Cloud Build Runs** - Actual build executions (last 30 days)
- **Artifact Registry** - Docker, Maven, npm repositories
- **Vertex AI Models** - Machine learning models
- **Vertex AI Endpoints** - Deployed ML model endpoints
- **Vertex AI Custom Jobs** - Training jobs and custom ML workloads

### Usage-Based APIs
The tool automatically detects and displays costs from usage-based APIs that don't have persistent resources:
- **Cloud Text-to-Speech API** - API calls for text-to-speech conversion
- **Gemini API** - API calls to Gemini language models
- **Vertex AI API Usage** - Prediction, embedding, and other API usage costs
- **Any other billable service** - Automatically discovered from billing data

These services appear in the resource list with type "X API Usage" and show the total cost from the last 90 days, even though they don't have listable resources like VMs or buckets.

## Output Format

### Resource Table

The tool displays resources in an ASCII table with the following columns:

- `#` - Resource index
- `Type` - Resource type
- `Name` - Resource name
- `Location` - GCP region/zone or "global"
- `MTD Cost` - Month-to-date cost (from billing export)
- `Status` - Current status (RUNNING, ACTIVE, etc.)
- `Details` - Additional information (size, version, tier, etc.)

### Summary Statistics

After the table, a summary section shows:
- Total resource count
- Month-to-date total cost
- Cost breakdown by resource type with MTD costs per type
- Total MTD cost across all resources

## Example Output

```
#     Type                   Name                         Location           MTD Cost     Status       Details
------------------------------------------------------------------------------------------------------------------------------------------------------
1     Compute Engine VM      web-server-1                 us-central1-a      $156.23      RUNNING      Type: n1-standard-1
2     Compute Engine VM      app-server-2                 us-east1-b         $312.45      RUNNING      Type: n2-standard-2
3     Persistent Disk        boot-disk-1                  us-central1-a      $12.50       READY        Size: 100 GB
4     Cloud Storage Bucket   my-data-bucket               US                 $8.75        ACTIVE       Class: STANDARD
5     Cloud SQL Instance     prod-db                      us-central1        $425.00      RUNNABLE     Version: POSTGRES_14, Tier: db-n1-standard-1
6     GKE Cluster            prod-cluster                 us-central1        $876.50      RUNNING      Nodes: 3, Version: 1.27
7     Load Balancer          web-lb                       us-central1        $45.00       ACTIVE       Type: EXTERNAL
8     Cloud DNS Zone         example-zone                 global             $0.50        ACTIVE       Domain: example.com.
9     Reserved IP            static-ip-1                  us-central1        $3.60        RESERVED     IP: 34.123.45.67
...

────────────────────────────────────────────────────────────────────────────────
SUMMARY: 42 total resources | Month-to-Date Cost: $1,840.53
────────────────────────────────────────────────────────────────────────────────
Resource Type                       Count        MTD Cost
------------------------------------------------------------
Compute Engine VM                       5         $468.68
Persistent Disk                         8          $98.40
Cloud Storage Bucket                   12          $24.50
Cloud SQL Instance                      2         $850.00
GKE Cluster                             1         $876.50
Load Balancer                           3         $135.00
VPN Gateway                             1          $36.00
Cloud NAT                               2          $88.00
Memorystore Redis                       1         $125.45
Cloud DNS Zone                          4           $2.00
Reserved IP                             2           $7.20
Cloud Function                          6          $45.30
Cloud Run Service                       3          $83.50
────────────────────────────────────────────────────────────────────────────────
💰 Total Cost (Last 90 Days): $1,840.53 USD

Note: Costs are fetched from BigQuery billing export (last 90 days)
```

## Performance

- **Parallel Fetching**: All 21 resource types are fetched concurrently using goroutines
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
cloudbuild.builds.list
cloudbuild.triggers.list
artifactregistry.repositories.list
aiplatform.models.list
aiplatform.endpoints.list
aiplatform.customJobs.list
```

## Dependencies

- **Google Cloud Go SDKs** - Official GCP client libraries
  - cloud.google.com/go/compute
  - cloud.google.com/go/storage
  - cloud.google.com/go/bigquery
  - cloud.google.com/go/functions
  - cloud.google.com/go/run
  - cloud.google.com/go/pubsub
  - cloud.google.com/go/redis
  - cloud.google.com/go/billing
  - cloud.google.com/go/aiplatform
  - cloud.google.com/go/artifactregistry
  - cloud.google.com/go/cloudbuild
  - google.golang.org/api/sqladmin
  - google.golang.org/api/container
  - google.golang.org/api/dns

## Cost Tracking (Last 90 Days)

The tool displays **actual costs from the last 90 days** for each resource by querying BigQuery billing export data.

### Setup Instructions

To enable cost tracking:

1. **Enable BigQuery Billing Export** in your GCP project:
   - Go to [Cloud Billing Export Settings](https://console.cloud.google.com/billing/export)
   - Click "Edit Settings" for BigQuery export
   - Select or create a BigQuery dataset for billing data
   - Enable "Standard usage cost" export
   - Wait 24 hours for initial data to populate

2. **Grant BigQuery permissions** to your service account:
   ```bash
   # Grant BigQuery Data Viewer role
   gcloud projects add-iam-policy-binding PROJECT_ID \
     --member="serviceAccount:SERVICE_ACCOUNT_EMAIL" \
     --role="roles/bigquery.dataViewer"

   # Grant BigQuery Job User role (to run queries)
   gcloud projects add-iam-policy-binding PROJECT_ID \
     --member="serviceAccount:SERVICE_ACCOUNT_EMAIL" \
     --role="roles/bigquery.jobUser"
   ```

3. **Run the tool** - costs will be automatically fetched and displayed!

### How It Works

- Queries BigQuery billing export for **last 90 days** of costs
- Maps costs to resources by service type and location
- Shows **per-resource costs** in the main table
- Displays **cost breakdown by resource type** in summary
- Automatically discovers billing export tables with standard naming patterns
- **Creates virtual resources** for usage-based APIs (Text-to-Speech, Gemini, etc.)

### Without Billing Export

If BigQuery billing export is not enabled, the tool will:
- Show resources without cost data (displayed as "-")
- Display a message with instructions to enable billing export
- Continue to work normally for resource listing

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
