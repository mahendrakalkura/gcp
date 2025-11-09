#!/bin/bash
# Build script for GCP Resource Lister

set -e

echo "Building GCP Resource Lister..."

# Build the binary
go build -o main .

echo "✓ Build complete: ./main"
