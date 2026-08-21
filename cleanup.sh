#!/bin/bash
# Remove redundant files - keep only the simple stuff

echo "🧹 Removing redundant files..."

# Remove complex Kubernetes manifests
rm -rf manifests/

# Remove redundant Docker files
rm -f Dockerfile docker-compose.yml .dockerignore

# Remove complex deployment scripts
rm -f deploy.sh KUBERNETES.md

echo "✅ Cleanup complete!"
echo ""
echo "Kept files:"
ls -1 | grep -v cleanup.sh
