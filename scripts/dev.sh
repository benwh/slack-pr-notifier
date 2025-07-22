#!/bin/bash

set -euo pipefail

# Cleanup function
cleanup() {
    echo ""
    echo "🧹 Cleaning up..."
    kill $WATCHEXEC_PID $NGROK_PID 2>/dev/null || true
    exit 0
}

# Set up trap early
trap cleanup SIGINT SIGTERM

echo "🚀 Starting local development environment..."

# Create tmp directory if it doesn't exist
mkdir -p tmp

# Truncate the log file at startup
true > tmp/app.log
echo "📝 Logging output to tmp/app.log"

# Check if watchexec is installed
if ! command -v watchexec &> /dev/null; then
    echo "❌ watchexec not found. Please install it:"
    echo "   brew install watchexec"
    echo "   or download from https://github.com/watchexec/watchexec"
    exit 1
fi

# Check if ngrok is installed
if ! command -v ngrok &> /dev/null; then
    echo "❌ ngrok not found. Please install it:"
    echo "   brew install ngrok"
    echo "   or download from https://ngrok.com"
    exit 1
fi

# Check if environment file exists
if [ ! -f ".env" ]; then
    echo "❌ .env file not found. Please create it:"
    echo "   cp .env.example .env"
    echo "   # Edit .env with your configuration"
    exit 1
fi

# Load environment variables
set -a
# shellcheck disable=SC1091
source .env
set +a

# Set default port if not specified
PORT=${PORT:-8080}

# Check if ngrok domain is specified
if [ -z "$NGROK_DOMAIN" ]; then
    echo "❌ NGROK_DOMAIN must be specified in .env file"
    echo "   Add: NGROK_DOMAIN=your-domain.ngrok-free.app"
    exit 1
fi

# Check if port is already in use and kill the process
if lsof -ti:"$PORT" >/dev/null 2>&1; then
    echo "🔄 Port $PORT is in use, killing existing processes..."
    lsof -ti:"$PORT" | xargs kill -9 2>/dev/null || true
    sleep 2
fi

# Start the Go application with hot-reload using watchexec
echo "🔧 Starting Go application with hot-reload on port $PORT..."
if ! watchexec \
    --restart \
    --watch . \
    --exts go,mod \
    --ignore .git/ \
    --ignore tmp/ \
    --ignore logs/ \
    --debounce 1s \
    "echo '🔄 Rebuilding application...' && go run ./cmd/github-slack-notifier" 2>&1 | tee -a tmp/app.log & then
    echo "❌ Failed to start watchexec"
    exit 1
fi
WATCHEXEC_PID=$!

# Wait for the application to start
echo "⏳ Waiting for application to start..."
sleep 5

# Check if watchexec process is still running
if ! kill -0 $WATCHEXEC_PID 2>/dev/null; then
    echo "❌ watchexec process died"
    exit 1
fi

# Check if application started successfully
RETRIES=0
MAX_RETRIES=10
while [ $RETRIES -lt $MAX_RETRIES ]; do
    if curl -s "http://localhost:$PORT/health" > /dev/null; then
        echo "✅ Application started on port $PORT"
        break
    fi
    echo "⏳ Waiting for application to be ready... (attempt $((RETRIES + 1))/$MAX_RETRIES)"
    sleep 2
    RETRIES=$((RETRIES + 1))
done

if [ $RETRIES -eq $MAX_RETRIES ]; then
    echo "❌ Application failed to start (health check failed after $MAX_RETRIES attempts)"
    kill $WATCHEXEC_PID 2>/dev/null || true
    exit 1
fi

# Start ngrok tunnel
echo "🌐 Starting ngrok tunnel with domain $NGROK_DOMAIN..."
if ! ngrok http "$PORT" --domain="$NGROK_DOMAIN" --log=stdout 2>&1 | tee -a tmp/app.log > /dev/null & then
    echo "❌ Failed to start ngrok"
    kill $WATCHEXEC_PID 2>/dev/null || true
    exit 1
fi
NGROK_PID=$!

# Wait for ngrok to start
sleep 3

# Check if ngrok process is still running
if ! kill -0 $NGROK_PID 2>/dev/null; then
    echo "❌ ngrok process died"
    kill $WATCHEXEC_PID 2>/dev/null || true
    exit 1
fi

# Use the configured domain URL
NGROK_URL="https://$NGROK_DOMAIN"

# Verify the tunnel is working
if ! curl -s "$NGROK_URL" > /dev/null; then
    echo "❌ ngrok tunnel not accessible at $NGROK_URL"
    kill $WATCHEXEC_PID $NGROK_PID 2>/dev/null || true
    exit 1
fi

echo "🎉 Development environment ready!"
echo "📍 Local URL:  http://localhost:$PORT"
echo "🌍 Public URL: $NGROK_URL"
echo ""
echo "🔗 Webhook URLs:"
echo "   GitHub: $NGROK_URL/webhooks/github"
echo "   Slack:  $NGROK_URL/webhooks/slack"
echo ""
echo "📊 ngrok dashboard: http://localhost:4040"
echo ""
echo "🔄 Hot-reload is enabled - the app will automatically restart when you modify .go files"
echo ""
echo "Press Ctrl+C to stop..."


# Keep the script running until interrupted
while true; do
    sleep 1
done
