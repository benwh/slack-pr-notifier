#!/bin/bash

set -e

PROJECT_ID="incident-io-dev-ben"

echo "🔥 Setting up Firestore for project: $PROJECT_ID"

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
gcloud config set project $PROJECT_ID

# Enable required APIs
echo "🔧 Enabling required APIs..."
gcloud services enable firestore.googleapis.com
gcloud services enable cloudbuild.googleapis.com
gcloud services enable run.googleapis.com
gcloud services enable artifactregistry.googleapis.com

# Create Firestore database
echo "🗄️  Creating Firestore database..."
if gcloud firestore databases describe --location=us-central1 2>/dev/null; then
    echo "✅ Firestore database already exists"
else
    gcloud firestore databases create --location=us-central1 --type=firestore-native
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

gcloud firestore indexes composite create --file=firestore.indexes.json
rm firestore.indexes.json

# Create Artifact Registry repository
echo "🏺 Creating Artifact Registry repository..."
REGION="us-central1"
REPO_NAME="github-slack-notifier"

if gcloud artifacts repositories describe $REPO_NAME --location=$REGION 2>/dev/null; then
    echo "✅ Artifact Registry repository already exists"
else
    gcloud artifacts repositories create $REPO_NAME \
        --repository-format=docker \
        --location=$REGION \
        --description="Docker images for GitHub Slack Notifier"
    echo "✅ Artifact Registry repository created"
fi

# Configure Docker authentication
echo "🐳 Configuring Docker authentication..."
gcloud auth configure-docker $REGION-docker.pkg.dev

echo "🎉 Firestore and infrastructure setup complete!"
echo ""
echo "📝 Next steps:"
echo "1. Configure your environment variables in .env"
echo "2. Run './scripts/dev.sh' for local development"
echo "3. Run './scripts/deploy.sh' to deploy to Cloud Run"