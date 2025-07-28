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

# Use project flag instead of setting global config
echo "📋 Using project: $PROJECT_ID"

# Enable required APIs
echo "🔧 Enabling required APIs..."
gcloud services enable firestore.googleapis.com --project="$PROJECT_ID"
gcloud services enable cloudbuild.googleapis.com --project="$PROJECT_ID"
gcloud services enable run.googleapis.com --project="$PROJECT_ID"
gcloud services enable artifactregistry.googleapis.com --project="$PROJECT_ID"
gcloud services enable cloudtasks.googleapis.com --project="$PROJECT_ID"
gcloud services enable secretmanager.googleapis.com --project="$PROJECT_ID"

# Create Firestore database
echo "🗄️  Creating Firestore database..."
if gcloud firestore databases describe --database="$DATABASE_ID" --project="$PROJECT_ID" 2>/dev/null; then
    echo "✅ Firestore database already exists"
else
    gcloud firestore databases create --database="$DATABASE_ID" --location="$REGION" --type=firestore-native --project="$PROJECT_ID"
    echo "✅ Firestore database created"
fi

# Create indexes
echo "📚 Creating Firestore indexes..."

# Create composite index for messages collection
echo "   Creating index for messages collection..."
gcloud firestore indexes composite create \
    --collection-group=messages \
    --field-config=field-path=repo_full_name,order=ascending \
    --field-config=field-path=pr_number,order=ascending \
    --database="$DATABASE_ID" \
    --project="$PROJECT_ID" \
    --quiet || echo "   Index may already exist"

# Create composite index for users collection
echo "   Creating index for users collection..."
gcloud firestore indexes composite create \
    --collection-group=users \
    --field-config=field-path=slack_user_id,order=ascending \
    --database="$DATABASE_ID" \
    --project="$PROJECT_ID" \
    --quiet || echo "   Index may already exist"

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
echo "1. Configure your environment variables in .env"
echo "2. Run './scripts/dev.sh' for local development"
echo "3. Run './scripts/deploy.sh <your-env-file>' to deploy to Cloud Run"
