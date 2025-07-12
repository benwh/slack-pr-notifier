#!/bin/bash

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
SERVICE_NAME="github-slack-notifier"
REPO_NAME="github-slack-notifier"
IMAGE_NAME="${REGION}-docker.pkg.dev/${PROJECT_ID}/${REPO_NAME}/${SERVICE_NAME}"

echo "🚀 Deploying GitHub-Slack Notifier to GCP..."
echo "📊 Project: $PROJECT_ID"
echo "📊 Database: $DATABASE_ID"
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
gcloud config set project "$PROJECT_ID"

# Configure Docker authentication
gcloud auth configure-docker "$REGION-docker.pkg.dev"

echo "📦 Building Docker image..."
docker build -t "${IMAGE_NAME}" .

echo "🔄 Pushing image to Artifact Registry..."
docker push "${IMAGE_NAME}"

echo "☁️  Deploying to Cloud Run..."
gcloud run deploy "${SERVICE_NAME}" \
  --image="${IMAGE_NAME}" \
  --platform=managed \
  --region="${REGION}" \
  --allow-unauthenticated \
  --port=8080 \
  --memory=1Gi \
  --cpu=1 \
  --max-instances=10 \
  --set-env-vars="FIRESTORE_PROJECT_ID=${PROJECT_ID},FIRESTORE_DATABASE_ID=${DATABASE_ID},GCP_REGION=${REGION}" \
  --project="${PROJECT_ID}"

echo "🔧 Getting service URL..."
SERVICE_URL=$(gcloud run services describe "${SERVICE_NAME}" --platform=managed --region="${REGION}" --format="value(status.url)" --project="${PROJECT_ID}")

echo "✅ Deployment complete!"
echo "📍 Service URL: ${SERVICE_URL}"
echo ""
echo "🔗 Webhook URLs:"
echo "   GitHub: ${SERVICE_URL}/webhooks/github"
echo "   Slack:  ${SERVICE_URL}/webhooks/slack"
echo ""
echo "ℹ️  See README.md for detailed setup instructions"