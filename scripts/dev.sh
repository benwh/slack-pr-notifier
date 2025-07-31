#!/usr/bin/env bash

set -euo pipefail

# Initialize PID variables
WATCHEXEC_PID=""
NGROK_PID=""

# Cleanup function
cleanup() {
    echo ""
    echo "🧹 Cleaning up..."

    # Kill watchexec and its children (go run processes)
    if [ -n "$WATCHEXEC_PID" ]; then
        echo "🔄 Stopping watchexec and Go processes..."
        # Kill watchexec process and its children
        kill -TERM $WATCHEXEC_PID 2>/dev/null || true
        # Also kill any remaining go run processes as backup
        pkill -f "go run.*github-slack-notifier" 2>/dev/null || true
        # Kill any watchexec processes
        pkill -f "watchexec.*github-slack-notifier" 2>/dev/null || true
        # Wait a moment for graceful shutdown
        sleep 1
        # Force kill if still running
        kill -KILL $WATCHEXEC_PID 2>/dev/null || true
    fi

    # Kill ngrok
    if [ -n "$NGROK_PID" ]; then
        echo "🌐 Stopping ngrok..."
        kill -TERM $NGROK_PID 2>/dev/null || true
        sleep 1
        kill -KILL $NGROK_PID 2>/dev/null || true
    fi

    # Final cleanup - kill any remaining processes on our port
    if [ -n "$PORT" ] && lsof -ti:"$PORT" >/dev/null 2>&1; then
        echo "🔄 Killing remaining processes on port $PORT..."
        lsof -ti:"$PORT" | xargs kill -9 2>/dev/null || true
    fi

    echo "✅ Cleanup complete"
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

# Kill any existing ngrok processes to avoid conflicts
if pgrep -f "ngrok.*$PORT" >/dev/null 2>&1; then
    echo "🔄 Killing existing ngrok processes..."
    pkill -f "ngrok.*$PORT" 2>/dev/null || true
    sleep 2
fi

# Start the Go application with hot-reload using watchexec
echo "🔧 Starting Go application with hot-reload on port $PORT..."
# Start watchexec in background with job control
set -m  # Enable job control
watchexec \
    --restart \
    --watch . \
    --exts go,mod \
    --ignore .git/ \
    --ignore tmp/ \
    --ignore logs/ \
    --ignore scripts/ \
    --debounce 1s \
    "echo '🔄 Rebuilding application...' && go run ./cmd/github-slack-notifier" 2>&1 | tee -a tmp/app.log &
WATCHEXEC_PID=$!
set +m  # Disable job control to avoid job control messages

# Wait for the application to start (longer initial wait to account for watchexec restarts)
echo "⏳ Waiting for application to start..."
sleep 12

# Check if watchexec process is still running
if ! kill -0 $WATCHEXEC_PID 2>/dev/null; then
    echo "❌ watchexec process died"
    exit 1
fi

# Check if application started successfully (longer timeout to allow for multiple watchexec retries)
RETRIES=0
MAX_RETRIES=30
echo "🔍 Checking application health..."
while [ $RETRIES -lt $MAX_RETRIES ]; do
    # First check if watchexec is still running
    if ! kill -0 $WATCHEXEC_PID 2>/dev/null; then
        echo "❌ watchexec process died"
        exit 1
    fi

    if curl -s "http://localhost:$PORT/health" > /dev/null; then
        echo "✅ Application started on port $PORT"
        break
    fi

    # Only show periodic progress to avoid spam
    if [ $((RETRIES % 5)) -eq 0 ] || [ $RETRIES -eq 0 ]; then
        echo "⏳ Waiting for application to be ready... (attempt $((RETRIES + 1))/$MAX_RETRIES)"
    fi
    sleep 2
    RETRIES=$((RETRIES + 1))
done

if [ $RETRIES -eq $MAX_RETRIES ]; then
    echo "❌ Application failed to start (health check failed after $MAX_RETRIES attempts)"
    echo "💡 Check tmp/app.log for compilation errors or startup issues"
    [ -n "$WATCHEXEC_PID" ] && kill $WATCHEXEC_PID 2>/dev/null || true
    exit 1
fi

# Start ngrok tunnel
echo "🌐 Starting ngrok tunnel with domain $NGROK_DOMAIN..."
# Start ngrok in background with job control
set -m  # Enable job control
ngrok http "$PORT" --domain="$NGROK_DOMAIN" --log=stdout >> tmp/app.log 2>&1 &
NGROK_PID=$!
set +m  # Disable job control to avoid job control messages

# Wait for ngrok to start
sleep 3

# Check if ngrok process is still running
if ! kill -0 $NGROK_PID 2>/dev/null; then
    echo "❌ ngrok process died"
    [ -n "$WATCHEXEC_PID" ] && kill $WATCHEXEC_PID 2>/dev/null || true
    exit 1
fi

# Use the configured domain URL
NGROK_URL="https://$NGROK_DOMAIN"

# Verify the tunnel is working
if ! curl -s "$NGROK_URL" > /dev/null; then
    echo "❌ ngrok tunnel not accessible at $NGROK_URL"
    [ -n "$WATCHEXEC_PID" ] && kill $WATCHEXEC_PID 2>/dev/null || true
    [ -n "$NGROK_PID" ] && kill $NGROK_PID 2>/dev/null || true
    exit 1
fi

echo "🎉 Development environment ready!"
echo "📍 Local URL:  http://localhost:$PORT"
echo "🌍 Public URL: $NGROK_URL"
echo ""
echo "📊 ngrok dashboard: http://localhost:4040"
echo ""
echo "🔄 Hot-reload is enabled - the app will automatically restart when you modify .go files"
echo ""
echo "Press Ctrl+C to stop..."


# Keep the script running until interrupted
# Use wait instead of sleep loop to be more responsive to signals
while true; do
    # Check if our background processes are still running
    if ! kill -0 $WATCHEXEC_PID 2>/dev/null; then
        echo "❌ watchexec process died unexpectedly"
        cleanup
    fi

    if ! kill -0 $NGROK_PID 2>/dev/null; then
        echo "❌ ngrok process died unexpectedly"
        cleanup
    fi

    # Sleep but make it interruptible
    sleep 5 &
    wait $!
done
