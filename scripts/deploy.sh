#!/bin/bash

set -e

PROJECT_ID="incident-io-dev-ben"
SERVICE_NAME="github-slack-notifier"
REGION="us-central1"
REPO_NAME="github-slack-notifier"
IMAGE_NAME="${REGION}-docker.pkg.dev/${PROJECT_ID}/${REPO_NAME}/${SERVICE_NAME}"

echo "🚀 Deploying GitHub-Slack Notifier to GCP..."

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
gcloud config set project $PROJECT_ID

# Configure Docker authentication
gcloud auth configure-docker $REGION-docker.pkg.dev

echo "📦 Building Docker image..."
docker build -t ${IMAGE_NAME} .

echo "🔄 Pushing image to Artifact Registry..."
docker push ${IMAGE_NAME}

echo "☁️  Deploying to Cloud Run..."
gcloud run deploy ${SERVICE_NAME} \
  --image=${IMAGE_NAME} \
  --platform=managed \
  --region=${REGION} \
  --allow-unauthenticated \
  --port=8080 \
  --memory=1Gi \
  --cpu=1 \
  --max-instances=10 \
  --set-env-vars="FIRESTORE_PROJECT_ID=${PROJECT_ID}" \
  --project=${PROJECT_ID}

echo "🔧 Getting service URL..."
SERVICE_URL=$(gcloud run services describe ${SERVICE_NAME} --platform=managed --region=${REGION} --format="value(status.url)" --project=${PROJECT_ID})

echo "✅ Deployment complete!"
echo "📍 Service URL: ${SERVICE_URL}"
echo ""
echo "🔗 Webhook URLs:"
echo "   GitHub: ${SERVICE_URL}/webhooks/github"
echo "   Slack:  ${SERVICE_URL}/webhooks/slack"
echo ""
echo "ℹ️  See README.md for detailed setup instructions"