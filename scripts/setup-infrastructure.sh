#!/usr/bin/env bash

# Setup GCP infrastructure for GitHub-Slack Notifier
# This script creates:
# - Firestore database
# - Artifact Registry repository
# - Required API enablements
# - Docker authentication configuration

set -euo pipefail

# Check if env file argument is provided
if [ $# -eq 0 ]; then
    echo "❌ Usage: $0 <env-file>"
    echo "   Example: $0 production.env"
    echo "   Example: $0 staging.env"
    exit 1
fi

ENV_FILE="$1"

# Check if env file exists
if [ ! -f "$ENV_FILE" ]; then
    echo "❌ Environment file '$ENV_FILE' not found"
    echo "   Please create it based on .env.example"
    exit 1
fi

# Load environment variables from specified env file
echo "📋 Loading environment variables from $ENV_FILE..."
set -a
# shellcheck source=/dev/null
source "$ENV_FILE"
set +a

# Check required environment variables
if [ -z "$FIRESTORE_PROJECT_ID" ]; then
    echo "❌ FIRESTORE_PROJECT_ID must be set in $ENV_FILE"
    exit 1
fi

if [ -z "$FIRESTORE_DATABASE_ID" ]; then
    echo "❌ FIRESTORE_DATABASE_ID must be set in $ENV_FILE"
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

# Use project flag instead of setting global config
echo "📋 Using project: $PROJECT_ID"

# Enable required APIs (idempotent - safe to re-run)
echo "🔧 Enabling required APIs..."
APIS=(
    "firestore.googleapis.com"
    "cloudbuild.googleapis.com"
    "run.googleapis.com"
    "artifactregistry.googleapis.com"
    "cloudtasks.googleapis.com"
    "secretmanager.googleapis.com"
)

for api in "${APIS[@]}"; do
    if gcloud services list --enabled --filter="name:$api" --format="value(name)" --project="$PROJECT_ID" | grep -q "$api"; then
        echo "   ✅ $api already enabled"
    else
        echo "   Enabling $api..."
        gcloud services enable "$api" --project="$PROJECT_ID"
        echo "   ✅ $api enabled"
    fi
done

# Create Firestore database
echo "🗄️  Creating Firestore database..."
if gcloud firestore databases describe --database="$DATABASE_ID" --project="$PROJECT_ID" 2>/dev/null; then
    echo "✅ Firestore database already exists"
else
    gcloud firestore databases create --database="$DATABASE_ID" --location="$REGION" --type=firestore-native --project="$PROJECT_ID"
    echo "✅ Firestore database created"
fi

# Deploy Firestore indexes using dedicated script
echo "📚 Deploying Firestore indexes..."
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [ -f "$SCRIPT_DIR/deploy-firestore-indexes.sh" ]; then
    echo "   Running deploy-firestore-indexes.sh..."
    "$SCRIPT_DIR/deploy-firestore-indexes.sh" "$ENV_FILE"
else
    echo "   ⚠️  deploy-firestore-indexes.sh not found, skipping index deployment"
    echo "   You can deploy indexes manually with: ./scripts/deploy-firestore-indexes.sh $ENV_FILE"
fi

# Create Artifact Registry repository
echo "🏺 Creating Artifact Registry repository..."
REPO_NAME="github-slack-notifier"

if gcloud artifacts repositories describe "$REPO_NAME" --location="$REGION" --project="$PROJECT_ID" 2>/dev/null; then
    echo "✅ Artifact Registry repository already exists"
else
    gcloud artifacts repositories create "$REPO_NAME" \
        --repository-format=docker \
        --location="$REGION" \
        --description="Docker images for GitHub Slack Notifier" \
        --project="$PROJECT_ID"
    echo "✅ Artifact Registry repository created"
fi

# Configure Docker authentication
echo "🐳 Configuring Docker authentication..."
gcloud auth configure-docker "$REGION-docker.pkg.dev" --project="$PROJECT_ID"

# Create service account for the application
echo "🔐 Creating service account..."
SERVICE_NAME="github-slack-notifier"
SERVICE_ACCOUNT_NAME="$SERVICE_NAME"
SERVICE_ACCOUNT_EMAIL="$SERVICE_ACCOUNT_NAME@$PROJECT_ID.iam.gserviceaccount.com"

if gcloud iam service-accounts describe "$SERVICE_ACCOUNT_EMAIL" --project="$PROJECT_ID" 2>/dev/null; then
    echo "✅ Service account already exists"
else
    gcloud iam service-accounts create "$SERVICE_ACCOUNT_NAME" \
        --display-name="Service account for $SERVICE_NAME" \
        --description="Service account used by the GitHub Slack Notifier application" \
        --project="$PROJECT_ID"
    echo "✅ Service account created: $SERVICE_ACCOUNT_EMAIL"
fi

# Grant required IAM permissions to the service account
echo "🔑 Granting IAM permissions to service account..."

# Required roles for the application:
# - datastore.user: Read/write access to Firestore
# - cloudtasks.enqueuer: Ability to enqueue Cloud Tasks
# - run.invoker: Ability to invoke Cloud Run services (for task processing)
REQUIRED_ROLES=(
    "roles/datastore.user"
    "roles/cloudtasks.enqueuer" 
    "roles/run.invoker"
)

for role in "${REQUIRED_ROLES[@]}"; do
    echo "   Granting $role..."
    gcloud projects add-iam-policy-binding "$PROJECT_ID" \
        --member="serviceAccount:$SERVICE_ACCOUNT_EMAIL" \
        --role="$role" \
        --project="$PROJECT_ID" \
        --condition=None \
        --quiet
    echo "   ✅ $role granted"
done

# Define which variables should be stored as secrets in Secret Manager
SECRET_VARS=(
    "GITHUB_WEBHOOK_SECRET"
    "SLACK_SIGNING_SECRET"
    "SLACK_CLIENT_SECRET"
    "GITHUB_CLIENT_SECRET"
    "CLOUD_TASKS_SECRET"
)

# Grant secretAccessor role for each secret that exists
echo "🔑 Granting secret access permissions..."
for secret_var in "${SECRET_VARS[@]}"; do
    # Check if the secret variable has a value
    secret_value="${!secret_var:-}"
    if [ -n "$secret_value" ]; then
        echo "   Granting access to secret: ${secret_var}"
        gcloud secrets add-iam-policy-binding "${secret_var}" \
            --member="serviceAccount:${SERVICE_ACCOUNT_EMAIL}" \
            --role="roles/secretmanager.secretAccessor" \
            --project="${PROJECT_ID}" >/dev/null
    fi
done

# Create Cloud Tasks queue (for async processing)
echo "📤 Creating Cloud Tasks queue..."

# Set Cloud Tasks configuration from .env or defaults
if [ -z "$CLOUD_TASKS_QUEUE" ]; then
    CLOUD_TASKS_QUEUE="webhook-processing"
fi

# Use GCP_REGION for Cloud Tasks location (already set above)
CLOUD_TASKS_LOCATION="$REGION"

echo "   Queue name: $CLOUD_TASKS_QUEUE"
echo "   Location: $CLOUD_TASKS_LOCATION"

if gcloud tasks queues describe "$CLOUD_TASKS_QUEUE" --location="$CLOUD_TASKS_LOCATION" --project="$PROJECT_ID" 2>/dev/null; then
    echo "✅ Cloud Tasks queue already exists"
else
    # Create Cloud Tasks queue with retry configuration
    # IMPORTANT: These are queue-level defaults. The application enforces its own
    # retry limits by checking the retry count header and returning 200 OK to stop
    # retries when the configured max attempts is reached.
    #
    # Retry configuration explanation:
    # - max-attempts: Maximum number of attempts (including first attempt)
    # - max-retry-duration: Maximum time to keep retrying
    # - min-backoff: Initial delay between retries (doubles with each retry)
    # - max-backoff: Maximum delay between retries
    # - max-doublings: How many times to double the backoff before it plateaus
    #
    # With these settings, retry delays will be: 1s, 2s, 4s, 8s, 16s, 32s, 64s, 128s, 256s, 512s, 600s, 600s...
    # Total retry duration is capped at 2 days (172800s)
    gcloud tasks queues create "$CLOUD_TASKS_QUEUE" \
        --location="$CLOUD_TASKS_LOCATION" \
        --max-attempts=100 \
        --max-retry-duration=172800s \
        --min-backoff=1s \
        --max-backoff=600s \
        --max-doublings=10 \
        --max-concurrent-dispatches=10 \
        --max-dispatches-per-second=100 \
        --project="$PROJECT_ID"
    echo "✅ Cloud Tasks queue created"
fi

echo "🎉 GCP infrastructure setup complete!"
echo ""
echo "📝 Next steps:"
echo "1. Run './scripts/dev.sh' for local development"
echo "2. Run './scripts/deploy.sh $ENV_FILE' to deploy to Cloud Run"
