#!/bin/bash

set -e

PROJECT_ID="incident-io-dev-ben"
SERVICE_NAME="github-slack-notifier"
REGION="us-central1"
IMAGE_NAME="gcr.io/${PROJECT_ID}/${SERVICE_NAME}"

echo "🚀 Deploying GitHub-Slack Notifier to GCP..."

echo "📦 Building Docker image..."
docker build -t ${IMAGE_NAME} .

echo "🔄 Pushing image to Google Container Registry..."
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
echo "⚙️  Next steps:"
echo "1. Set up your environment variables in Cloud Run:"
echo "   gcloud run services update ${SERVICE_NAME} --region=${REGION} --project=${PROJECT_ID} \\"
echo "     --set-env-vars=\"SLACK_BOT_TOKEN=xoxb-your-token,GITHUB_WEBHOOK_SECRET=your-secret,SLACK_SIGNING_SECRET=your-secret,API_ADMIN_KEY=your-key\""
echo ""
echo "2. Configure GitHub webhook:"
echo "   - URL: ${SERVICE_URL}/webhooks/github"
echo "   - Events: Pull requests, Pull request reviews"
echo "   - Content type: application/json"
echo ""
echo "3. Configure Slack app:"
echo "   - Add slash commands pointing to: ${SERVICE_URL}/webhooks/slack"
echo "   - Commands: /notify-channel, /notify-link, /notify-status"
echo ""
echo "4. Enable Firestore in your GCP project if not already done"
echo "5. Register repositories using the admin API"