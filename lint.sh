#!/bin/bash
# Lint script for GCP Resource Lister

set -e

echo "Running linters..."

# Format check
echo "→ Checking code formatting..."
if ! gofmt -l . | grep -q .; then
    echo "  ✓ Code is properly formatted"
else
    echo "  ⚠ Found unformatted files:"
    gofmt -l .
    echo ""
    echo "  Running gofmt to fix..."
    gofmt -w .
    echo "  ✓ Files formatted"
fi

# Vet
echo "→ Running go vet..."
go vet ./...
echo "  ✓ go vet passed"

# Check for common issues
echo "→ Checking for unused imports..."
if command -v goimports &> /dev/null; then
    goimports -w .
    echo "  ✓ imports cleaned"
else
    echo "  ⚠ goimports not installed (optional)"
fi

echo ""
echo "✓ Linting complete"
