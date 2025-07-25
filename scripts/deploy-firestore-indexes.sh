#!/bin/bash

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Load environment variables
if [[ -f ".env" ]]; then
    # Export variables from .env, filtering out comments and blank lines
    export $(grep -v '^#' .env | grep -v '^$' | xargs)
    print_status "Loaded environment variables from .env"
else
    print_warning ".env file not found, using system environment variables"
fi

# Check if gcloud is installed
if ! command -v gcloud &> /dev/null; then
    print_error "gcloud CLI is not installed. Please install it first."
    exit 1
fi

# Check if firestore.indexes.json exists
if [[ ! -f "firestore.indexes.json" ]]; then
    print_error "firestore.indexes.json not found in current directory"
    exit 1
fi

# Use project ID from environment variables only
PROJECT_ID=${FIRESTORE_PROJECT_ID:-${GOOGLE_CLOUD_PROJECT:-}}
if [[ -z "$PROJECT_ID" ]]; then
    print_error "No GCP project found. Set FIRESTORE_PROJECT_ID or GOOGLE_CLOUD_PROJECT in .env"
    exit 1
fi

# Use database ID from environment or default
DATABASE_ID=${FIRESTORE_DATABASE_ID:-"(default)"}

print_status "Deploying Firestore indexes for project: $PROJECT_ID"
if [[ "$DATABASE_ID" != "(default)" ]]; then
    print_status "Using database: $DATABASE_ID"
fi

# Deploy the indexes
print_status "Deploying indexes from firestore.indexes.json..."

# Check if jq is available for JSON parsing
if ! command -v jq &> /dev/null; then
    print_error "jq is required for JSON parsing. Please install it first."
    exit 1
fi

# Function to get current indexes
get_current_indexes() {
    local current_indexes_file=$(mktemp)
    print_status "Fetching current indexes..."
    
    local list_cmd="gcloud firestore indexes composite list --project=$PROJECT_ID --format=json"
    if [[ "$DATABASE_ID" != "(default)" ]]; then
        list_cmd="$list_cmd --database=$DATABASE_ID"
    fi
    
    if eval "$list_cmd" > "$current_indexes_file" 2>/dev/null; then
        echo "$current_indexes_file"
    else
        print_error "Failed to fetch current indexes"
        rm -f "$current_indexes_file"
        exit 1
    fi
}

# Function to check if an index needs to be updated
index_needs_update() {
    local collection_group="$1"
    local desired_fields="$2"
    local query_scope="$3"
    local current_indexes_file="$4"
    
    # Check if the current indexes file exists and is readable
    if [[ ! -f "$current_indexes_file" ]]; then
        echo "create"
        return
    fi
    
    # Check if an index with the same collection group exists
    local existing_index=$(jq -r --arg cg "$collection_group" --arg qs "$query_scope" '
        .[] | select(.collectionGroup == $cg and (.queryScope // "COLLECTION") == $qs) | .name
    ' "$current_indexes_file" 2>/dev/null)
    
    if [[ -z "$existing_index" ]]; then
        # No existing index, needs creation
        echo "create"
        return
    fi
    
    # Compare field configurations (excluding __name__ field that Firestore adds automatically)
    local existing_fields=$(jq -r --arg cg "$collection_group" --arg qs "$query_scope" '
        .[] | select(.collectionGroup == $cg and (.queryScope // "COLLECTION") == $qs) | 
        .fields | map(select(.fieldPath != "__name__") | .fieldPath + ":" + (.order // "ASCENDING")) | sort | join(",")
    ' "$current_indexes_file" 2>/dev/null)
    
    local desired_fields_sorted=$(echo "$desired_fields" | tr ',' '\n' | sort | tr '\n' ',' | sed 's/,$//')
    
    if [[ "$existing_fields" != "$desired_fields_sorted" ]]; then
        # Fields differ, needs update (delete + create)
        echo "update:$existing_index"
    else
        # Index is identical, skip
        echo "skip"
    fi
}

# Function to delete an index
delete_index() {
    local index_name="$1"
    print_status "Deleting outdated index: $index_name"
    
    local delete_cmd="gcloud firestore indexes composite delete $index_name --quiet --project=$PROJECT_ID"
    if [[ "$DATABASE_ID" != "(default)" ]]; then
        delete_cmd="$delete_cmd --database=$DATABASE_ID"
    fi
    
    if eval "$delete_cmd"; then
        print_status "✓ Index deleted successfully"
        return 0
    else
        print_error "✗ Failed to delete index"
        return 1
    fi
}

# Get current indexes for comparison
CURRENT_INDEXES_FILE=$(get_current_indexes)

# Parse the JSON file and process indexes
INDEXES=$(jq -r '.indexes[] | @base64' firestore.indexes.json)
INDEX_COUNT=0
FAILED_COUNT=0
SKIPPED_COUNT=0
UPDATED_COUNT=0

for index in $INDEXES; do
    # Decode the base64 encoded JSON
    INDEX_DATA=$(echo "$index" | base64 --decode)
    
    # Extract index properties
    COLLECTION_GROUP=$(echo "$INDEX_DATA" | jq -r '.collectionGroup')
    QUERY_SCOPE=$(echo "$INDEX_DATA" | jq -r '.queryScope // "COLLECTION"')
    
    # Convert queryScope to lowercase for gcloud command
    case "$QUERY_SCOPE" in
        "COLLECTION")
            SCOPE_FLAG="collection"
            ;;
        "COLLECTION_GROUP")
            SCOPE_FLAG="collection-group"
            ;;
        *)
            SCOPE_FLAG="collection"
            ;;
    esac
    
    # Build field config arguments and track desired fields for comparison
    FIELD_CONFIGS=""
    DESIRED_FIELDS=""
    FIELDS=$(echo "$INDEX_DATA" | jq -r '.fields[] | @base64')
    for field in $FIELDS; do
        FIELD_DATA=$(echo "$field" | base64 --decode)
        FIELD_PATH=$(echo "$FIELD_DATA" | jq -r '.fieldPath')
        ORDER=$(echo "$FIELD_DATA" | jq -r '.order // "ASCENDING"')
        
        # Convert order to lowercase
        case "$ORDER" in
            "ASCENDING")
                ORDER_FLAG="ascending"
                ;;
            "DESCENDING")
                ORDER_FLAG="descending"
                ;;
            *)
                ORDER_FLAG="ascending"
                ;;
        esac
        
        if [[ -z "$FIELD_CONFIGS" ]]; then
            FIELD_CONFIGS="--field-config=field-path=$FIELD_PATH,order=$ORDER_FLAG"
            DESIRED_FIELDS="$FIELD_PATH:$ORDER"
        else
            FIELD_CONFIGS="$FIELD_CONFIGS --field-config=field-path=$FIELD_PATH,order=$ORDER_FLAG"
            DESIRED_FIELDS="$DESIRED_FIELDS,$FIELD_PATH:$ORDER"
        fi
    done
    
    # Check if index needs to be created, updated, or skipped
    INDEX_ACTION=$(index_needs_update "$COLLECTION_GROUP" "$DESIRED_FIELDS" "$QUERY_SCOPE" "$CURRENT_INDEXES_FILE")
    INDEX_COUNT=$((INDEX_COUNT + 1))
    
    case "$INDEX_ACTION" in
        "skip")
            print_status "⏭ Index $INDEX_COUNT for collection '$COLLECTION_GROUP' already exists and is up to date"
            SKIPPED_COUNT=$((SKIPPED_COUNT + 1))
            continue
            ;;
        "create")
            print_status "Creating new index $INDEX_COUNT for collection '$COLLECTION_GROUP'..."
            ;;
        update:*)
            INDEX_NAME_TO_DELETE="${INDEX_ACTION#update:}"
            print_status "Updating index $INDEX_COUNT for collection '$COLLECTION_GROUP'..."
            if delete_index "$INDEX_NAME_TO_DELETE"; then
                UPDATED_COUNT=$((UPDATED_COUNT + 1))
                print_status "Creating replacement index..."
            else
                print_error "✗ Failed to delete existing index, skipping creation"
                FAILED_COUNT=$((FAILED_COUNT + 1))
                continue
            fi
            ;;
    esac
    
    # Build the gcloud command
    DEPLOY_CMD="gcloud firestore indexes composite create --collection-group=$COLLECTION_GROUP --query-scope=$SCOPE_FLAG $FIELD_CONFIGS --project=$PROJECT_ID --async"
    if [[ "$DATABASE_ID" != "(default)" ]]; then
        DEPLOY_CMD="$DEPLOY_CMD --database=$DATABASE_ID"
    fi
    
    # Execute the command
    if output=$(eval "$DEPLOY_CMD 2>&1"); then
        print_status "✓ Index $INDEX_COUNT creation initiated successfully"
    else
        # Check if it's just because the index already exists
        if echo "$output" | grep -q "ALREADY_EXISTS"; then
            print_warning "⚠ Index $INDEX_COUNT already exists, skipping"
        elif echo "$output" | grep -q "must be configured with at least 2 fields"; then
            print_warning "⚠ Index $INDEX_COUNT is single-field, use field management instead"
        else
            print_error "✗ Failed to create index $INDEX_COUNT: $output"
            FAILED_COUNT=$((FAILED_COUNT + 1))
        fi
    fi
done

# Cleanup temporary file
rm -f "$CURRENT_INDEXES_FILE"

# Summary
CREATED_COUNT=$((INDEX_COUNT - SKIPPED_COUNT - FAILED_COUNT))
print_status ""
print_status "=== Index Deployment Summary ==="
print_status "Total indexes processed: $INDEX_COUNT"
print_status "Created: $CREATED_COUNT"
print_status "Updated: $UPDATED_COUNT"
print_status "Skipped (up to date): $SKIPPED_COUNT"
print_status "Failed: $FAILED_COUNT"

if [[ $FAILED_COUNT -eq 0 ]]; then
    print_status ""
    print_status "✅ Index deployment completed successfully!"
    
    if [[ $CREATED_COUNT -gt 0 || $UPDATED_COUNT -gt 0 ]]; then
        print_warning "Note: Index creation is asynchronous and may take several minutes to complete."
        print_status "Monitor progress with: gcloud firestore operations list --project=$PROJECT_ID"
        print_status "Or check the Firebase Console:"
        if [[ "$DATABASE_ID" == "(default)" ]]; then
            print_status "https://console.firebase.google.com/project/$PROJECT_ID/firestore/indexes"
        else
            print_status "https://console.firebase.google.com/project/$PROJECT_ID/firestore/databases/$DATABASE_ID/indexes"
        fi
    fi
else
    print_error "❌ Index deployment failed for $FAILED_COUNT out of $INDEX_COUNT indexes"
    exit 1
fi