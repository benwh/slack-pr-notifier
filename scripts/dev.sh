#!/bin/bash

set -e

# Cleanup function
cleanup() {
    echo ""
    echo "🧹 Cleaning up..."
    kill $APP_PID $NGROK_PID 2>/dev/null || true
    exit 0
}

# Set up trap early
trap cleanup SIGINT SIGTERM

echo "🚀 Starting local development environment..."

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
DEV_PORT=${DEV_PORT:-5005}

# Check if ngrok domain is specified
if [ -z "$NGROK_DOMAIN" ]; then
    echo "❌ NGROK_DOMAIN must be specified in .env file"
    echo "   Add: NGROK_DOMAIN=your-domain.ngrok-free.app"
    exit 1
fi

# Check if port is already in use and kill the process
if lsof -ti:$DEV_PORT >/dev/null 2>&1; then
    echo "🔄 Port $DEV_PORT is in use, killing existing processes..."
    lsof -ti:$DEV_PORT | xargs kill -9 2>/dev/null || true
    sleep 2
fi

# Start the Go application in the background
echo "🔧 Starting Go application on port $DEV_PORT..."
PORT=$DEV_PORT go run main.go &
APP_PID=$!

# Check if the command succeeded
if [ $? -ne 0 ]; then
    echo "❌ Failed to start Go application"
    exit 1
fi

# Wait for the application to start
sleep 3

# Check if the process is still running
if ! kill -0 $APP_PID 2>/dev/null; then
    echo "❌ Go application process died"
    exit 1
fi

# Check if application started successfully
if ! curl -s http://localhost:$DEV_PORT/health > /dev/null; then
    echo "❌ Application failed to start (health check failed)"
    kill $APP_PID 2>/dev/null || true
    exit 1
fi

echo "✅ Application started on port $DEV_PORT"

# Start ngrok tunnel
echo "🌐 Starting ngrok tunnel with domain $NGROK_DOMAIN..."
ngrok http $DEV_PORT --domain=$NGROK_DOMAIN --log=stdout > /dev/null 2>&1 &
NGROK_PID=$!

# Check if the command succeeded
if [ $? -ne 0 ]; then
    echo "❌ Failed to start ngrok"
    kill $APP_PID 2>/dev/null || true
    exit 1
fi

# Wait for ngrok to start
sleep 3

# Check if ngrok process is still running
if ! kill -0 $NGROK_PID 2>/dev/null; then
    echo "❌ ngrok process died"
    kill $APP_PID 2>/dev/null || true
    exit 1
fi

# Use the configured domain URL
NGROK_URL="https://$NGROK_DOMAIN"

# Verify the tunnel is working
if ! curl -s "$NGROK_URL" > /dev/null; then
    echo "❌ ngrok tunnel not accessible at $NGROK_URL"
    kill $APP_PID $NGROK_PID 2>/dev/null || true
    exit 1
fi

echo "🎉 Development environment ready!"
echo "📍 Local URL:  http://localhost:$DEV_PORT"
echo "🌍 Public URL: $NGROK_URL"
echo ""
echo "🔗 Webhook URLs:"
echo "   GitHub: $NGROK_URL/webhooks/github"
echo "   Slack:  $NGROK_URL/webhooks/slack"
echo ""
echo "📊 ngrok dashboard: http://localhost:4040"
echo ""
echo "Press Ctrl+C to stop..."


# Keep the script running until interrupted
while true; do
    sleep 1
done
