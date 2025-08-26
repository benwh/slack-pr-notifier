#!/usr/bin/env bash

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

# Capture existing environment variables before loading env file
declare -A EXISTING_ENV
while IFS='=' read -r key value; do
    EXISTING_ENV["$key"]=1
done < <(env | cut -d= -f1)

# Load environment variables from specified env file
echo "📋 Loading environment variables from $ENV_FILE..."
set -a
# shellcheck source=/dev/null
source "$ENV_FILE"
set +a

# Capture variables that were loaded from the env file
declare -A ENV_FILE_VARS
while IFS='=' read -r key value; do
    # Only track if it wasn't in the existing environment or if it was overridden
    if [[ ! -v EXISTING_ENV["$key"] ]] || [[ "$value" != "${!key}" ]]; then
        ENV_FILE_VARS["$key"]="$value"
    fi
done < <(grep -E '^[A-Z][A-Z0-9_]*=' "$ENV_FILE" | grep -v '^#')

# Define which variables should be stored as secrets in Secret Manager
SECRET_VARS=(
    "GITHUB_WEBHOOK_SECRET"
    "SLACK_SIGNING_SECRET"
    "SLACK_CLIENT_SECRET"
    "GITHUB_CLIENT_SECRET"
    "CLOUD_TASKS_SECRET"
)

# Function to check if a variable is a secret
is_secret_var() {
    local var_name="$1"
    for secret_var in "${SECRET_VARS[@]}"; do
        if [ "$secret_var" = "$var_name" ]; then
            return 0
        fi
    done
    return 1
}

# Function to create or update a secret in Secret Manager
create_or_update_secret() {
    local secret_name="$1"
    local secret_value="$2"
    local project_id="$3"

    echo "   Managing secret: $secret_name"

    # Check if secret exists
    if gcloud secrets describe "$secret_name" --project="$project_id" >/dev/null 2>&1; then
        # Secret exists, add new version
        echo "$secret_value" | gcloud secrets versions add "$secret_name" --data-file=- --project="$project_id"
    else
        # Secret doesn't exist, create it
        echo "$secret_value" | gcloud secrets create "$secret_name" --data-file=- --project="$project_id"
    fi
}

# Function to build environment variables string for Cloud Run
build_env_vars() {
    local env_vars=""

    # Only process variables that were loaded from the env file
    for var_name in "${!ENV_FILE_VARS[@]}"; do
        # Skip if this is a secret variable (will be handled separately)
        if is_secret_var "$var_name"; then
            continue
        fi

        # Skip internal script variables and Cloud Run reserved variables
        case "$var_name" in
            "ENV_FILE" | "PROJECT_ID" | "DATABASE_ID" | "REGION" | "SERVICE_NAME" | "REPO_NAME" | "IMAGE_NAME" | "SERVICE_URL" | "PORT")
                continue
                ;;
        esac

        # Get the current value of the variable
        var_value="${!var_name}"

        # Add to env vars string
        if [ -n "$env_vars" ]; then
            env_vars="${env_vars},"
        fi
        env_vars="${env_vars}${var_name}=${var_value}"
    done

    # Always include these core variables
    if [ -n "$env_vars" ]; then
        env_vars="${env_vars},"
    fi
    env_vars="${env_vars}FIRESTORE_PROJECT_ID=${PROJECT_ID},FIRESTORE_DATABASE_ID=${DATABASE_ID},GCP_REGION=${REGION}"

    echo "$env_vars"
}

# Function to build secrets string for Cloud Run
build_secrets() {
    local secrets=""

    for secret_var in "${SECRET_VARS[@]}"; do
        # Check if the secret variable has a value
        secret_value="${!secret_var:-}"
        if [ -n "$secret_value" ]; then
            if [ -n "$secrets" ]; then
                secrets="${secrets},"
            fi
            secrets="${secrets}${secret_var}=${secret_var}:latest"
        fi
    done

    echo "$secrets"
}

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

# Configure Docker authentication
gcloud auth configure-docker "$REGION-docker.pkg.dev"

echo "📦 Building Docker image..."
docker build -t "${IMAGE_NAME}" .

echo "🔄 Pushing image to Artifact Registry..."
docker push "${IMAGE_NAME}"

# Manage secrets in Secret Manager
echo "🔐 Managing secrets in Secret Manager..."
for secret_var in "${SECRET_VARS[@]}"; do
    # Use indirect variable expansion to get the value
    secret_value="${!secret_var:-}"
    if [ -n "$secret_value" ]; then
        create_or_update_secret "$secret_var" "$secret_value" "$PROJECT_ID"
    else
        echo "   Warning: $secret_var is empty or not set, skipping..."
    fi
done

# Service Account Management
echo "👤 Using service account..."
SERVICE_ACCOUNT_NAME="$SERVICE_NAME"
SERVICE_ACCOUNT_EMAIL="${SERVICE_ACCOUNT_NAME}@${PROJECT_ID}.iam.gserviceaccount.com"

# Verify service account exists (should be created by setup-infrastructure.sh)
if ! gcloud iam service-accounts describe "${SERVICE_ACCOUNT_EMAIL}" --project="${PROJECT_ID}" >/dev/null 2>&1; then
    echo "❌ Service account not found: ${SERVICE_ACCOUNT_NAME}"
    echo "   Please run: ./scripts/setup-infrastructure.sh $ENV_FILE"
    exit 1
else
    echo "✅ Service account exists: ${SERVICE_ACCOUNT_NAME}"
fi

# Grant secretAccessor role for each secret
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


echo "☁️  Deploying to Cloud Run..."

# Build environment variables and secrets for deployment
ENV_VARS=$(build_env_vars)
SECRETS=$(build_secrets)

# Build deployment command
DEPLOY_CMD="gcloud run deploy ${SERVICE_NAME} \
  --image=${IMAGE_NAME} \
  --platform=managed \
  --region=${REGION} \
  --allow-unauthenticated \
  --port=8080 \
  --memory=1Gi \
  --cpu=1 \
  --max-instances=10 \
  --service-account=${SERVICE_ACCOUNT_EMAIL} \
  --project=${PROJECT_ID}"

# Add environment variables if any
if [ -n "$ENV_VARS" ]; then
    DEPLOY_CMD="${DEPLOY_CMD} --set-env-vars=\"${ENV_VARS}\""
fi

# Add secrets if any
if [ -n "$SECRETS" ]; then
    DEPLOY_CMD="${DEPLOY_CMD} --set-secrets=\"${SECRETS}\""
fi

# Execute deployment
echo "   Environment variables: ${ENV_VARS}"
if [ -n "$SECRETS" ]; then
    echo "   Secrets: ${SECRETS}"
fi

eval "$DEPLOY_CMD"

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
