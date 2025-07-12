#!/bin/bash
#
# Setup GCP infrastructure for GitHub-Slack Notifier
# This script creates:
# - Firestore database
# - Artifact Registry repository  
# - Required API enablements
# - Docker authentication configuration

set -euo pipefail

# Load environment variables from .env file
if [ -f ".env" ]; then
    echo "📋 Loading environment variables from .env..."
    set -a
    # shellcheck disable=SC1091
    source .env
    set +a
else
    echo "❌ .env file not found. Please create it:"
    echo "   cp .env.example .env"
    echo "   # Edit .env with your configuration"
    exit 1
fi

# Check required environment variables
if [ -z "$FIRESTORE_PROJECT_ID" ]; then
    echo "❌ FIRESTORE_PROJECT_ID must be set in .env file"
    exit 1
fi

if [ -z "$FIRESTORE_DATABASE_ID" ]; then
    echo "❌ FIRESTORE_DATABASE_ID must be set in .env file"
    exit 1
fi

# Set default region if not specified
if [ -z "$GCP_REGION" ]; then
    GCP_REGION="europe-west1"
fi

PROJECT_ID="$FIRESTORE_PROJECT_ID"
DATABASE_ID="$FIRESTORE_DATABASE_ID"
REGION="$GCP_REGION"

echo "🏗️  Setting up GCP infrastructure for project: $PROJECT_ID"
echo "📊 Database ID: $DATABASE_ID"
echo "🌍 Region: $REGION"

# Check if gcloud is installed
if ! command -v gcloud &> /dev/null; then
    echo "❌ gcloud CLI not found. Please install it:"
    echo "   https://cloud.google.com/sdk/docs/install"
    exit 1
fi

# Check if user is authenticated
if ! gcloud auth list --filter=status:ACTIVE --format="value(account)" | grep -q .; then
    echo "❌ Not authenticated with gcloud. Please run:"
    echo "   gcloud auth login"
    exit 1
fi

# Set the project
echo "📋 Setting project to $PROJECT_ID..."
gcloud config set project "$PROJECT_ID"

# Enable required APIs
echo "🔧 Enabling required APIs..."
gcloud services enable firestore.googleapis.com
gcloud services enable cloudbuild.googleapis.com
gcloud services enable run.googleapis.com
gcloud services enable artifactregistry.googleapis.com

# Create Firestore database
echo "🗄️  Creating Firestore database..."
if gcloud firestore databases describe --database="$DATABASE_ID" --location="$REGION" 2>/dev/null; then
    echo "✅ Firestore database already exists"
else
    gcloud firestore databases create --database="$DATABASE_ID" --location="$REGION" --type=firestore-native
    echo "✅ Firestore database created"
fi

# Create indexes
echo "📚 Creating Firestore indexes..."
cat > firestore.indexes.json << EOF
{
  "indexes": [
    {
      "collectionGroup": "messages",
      "queryScope": "COLLECTION",
      "fields": [
        {
          "fieldPath": "repo_full_name",
          "order": "ASCENDING"
        },
        {
          "fieldPath": "pr_number",
          "order": "ASCENDING"
        }
      ]
    },
    {
      "collectionGroup": "users",
      "queryScope": "COLLECTION",
      "fields": [
        {
          "fieldPath": "slack_user_id",
          "order": "ASCENDING"
        }
      ]
    }
  ]
}
EOF

gcloud firestore indexes composite create --database="$DATABASE_ID" --file=firestore.indexes.json
rm firestore.indexes.json

# Create Artifact Registry repository
echo "🏺 Creating Artifact Registry repository..."
REPO_NAME="github-slack-notifier"

if gcloud artifacts repositories describe "$REPO_NAME" --location="$REGION" 2>/dev/null; then
    echo "✅ Artifact Registry repository already exists"
else
    gcloud artifacts repositories create "$REPO_NAME" \
        --repository-format=docker \
        --location="$REGION" \
        --description="Docker images for GitHub Slack Notifier"
    echo "✅ Artifact Registry repository created"
fi

# Configure Docker authentication
echo "🐳 Configuring Docker authentication..."
gcloud auth configure-docker "$REGION-docker.pkg.dev"

echo "🎉 GCP infrastructure setup complete!"
echo ""
echo "📝 Next steps:"
echo "1. Configure your environment variables in .env"
echo "2. Run './scripts/dev.sh' for local development"
echo "3. Run './scripts/deploy.sh' to deploy to Cloud Run"