#!/bin/bash
# Run script for GCP Resource Lister

set -e

# Check if google.json exists
if [ ! -f "google.json" ]; then
    echo "❌ Error: google.json not found"
    echo "Please add your GCP service account credentials as google.json"
    exit 1
fi

echo "Running GCP Resource Lister..."
echo ""

# Run directly with go run (no build needed)
go run main.go
