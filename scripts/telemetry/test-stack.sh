#!/bin/bash
# Test script for Drover telemetry stack
# Usage: ./scripts/telemetry/test-stack.sh

set -e

echo "🔍 Drover Telemetry Stack Test"
echo "================================"
echo ""

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Get the drover directory
DROVER_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$DROVER_DIR"

# Step 1: Check Docker is available
echo "1️⃣  Checking Docker availability..."
if ! command -v docker &> /dev/null; then
    echo -e "${RED}✗ Docker not found. Please install Docker first.${NC}"
    exit 1
fi
echo -e "${GREEN}✓ Docker found${NC}"

# Step 2: Check docker compose is available
echo ""
echo "2️⃣  Checking Docker Compose..."
if docker compose version &> /dev/null; then
    COMPOSE_CMD="docker compose"
elif docker-compose version &> /dev/null; then
    COMPOSE_CMD="docker-compose"
else
    echo -e "${RED}✗ Docker Compose not found${NC}"
    exit 1
fi
echo -e "${GREEN}✓ Using: $COMPOSE_CMD${NC}"

# Step 3: Build Drover
echo ""
echo "3️⃣  Building Drover..."
if ! go build -o drover ./cmd/drover; then
    echo -e "${RED}✗ Failed to build Drover${NC}"
    exit 1
fi
echo -e "${GREEN}✓ Drover built successfully${NC}"

# Step 4: Start telemetry stack
echo ""
echo "4️⃣  Starting telemetry stack..."
$COMPOSE_CMD -f docker-compose.telemetry.yaml up -d

# Wait for services to be healthy
echo ""
echo "5️⃣  Waiting for services to be healthy..."
sleep 5

# Check ClickHouse
echo ""
echo "6️⃣  Checking ClickHouse..."
max_attempts=30
attempt=0
while [ $attempt -lt $max_attempts ]; do
    if curl -s http://localhost:8123 > /dev/null 2>&1; then
        echo -e "${GREEN}✓ ClickHouse is ready${NC}"
        break
    fi
    attempt=$((attempt + 1))
    echo "   Waiting for ClickHouse... ($attempt/$max_attempts)"
    sleep 2
done

if [ $attempt -eq $max_attempts ]; then
    echo -e "${RED}✗ ClickHouse failed to start${NC}"
    $COMPOSE_CMD -f docker-compose.telemetry.yaml logs clickhouse
    exit 1
fi

# Check Collector
echo ""
echo "7️⃣  Checking OTel Collector..."
if ! curl -s http://localhost:13133 > /dev/null 2>&1; then
    echo -e "${RED}✗ Collector health check failed${NC}"
    $COMPOSE_CMD -f docker-compose.telemetry.yaml logs otel-collector
    exit 1
fi
echo -e "${GREEN}✓ Collector is running${NC}"

# Check Grafana
echo ""
echo "8️⃣  Checking Grafana..."
sleep 3
if ! curl -s http://localhost:3000 > /dev/null 2>&1; then
    echo -e "${YELLOW}⚠ Grafana might not be ready yet${NC}"
else
    echo -e "${GREEN}✓ Grafana is running${NC}"
fi

# Step 9: Create test project
echo ""
echo "9️⃣  Creating test project..."
TEST_DIR="/tmp/drover-telemetry-test-$$"
mkdir -p "$TEST_DIR"
cd "$TEST_DIR"

git init -q
git config user.email "test@test.com"
git config user.name "Test User"
git checkout -b main 2>/dev/null || git checkout -b main

echo "# Test Project" > README.md
git add README.md
git commit -m "Initial commit" -q

# Initialize Drover
echo ""
echo "🔟  Initializing Drover..."
if ! "$DROVER_DIR/drover" init > /dev/null 2>&1; then
    echo -e "${RED}✗ Failed to initialize Drover${NC}"
    exit 1
fi
echo -e "${GREEN}✓ Drover initialized${NC}"

# Add test tasks
echo ""
echo "1️⃣1️⃣  Adding test tasks..."
"$DROVER_DIR/drover" add "Test task 1" "First test task for telemetry" > /dev/null
"$DROVER_DIR/drover" add "Test task 2" "Second test task for telemetry" > /dev/null
"$DROVER_DIR/drover" add "Test task 3" "Third test task for telemetry" > /dev/null
echo -e "${GREEN}✓ Added 3 test tasks${NC}"

# Step 12: Run Drover with telemetry
echo ""
echo "1️⃣2️⃣  Running Drover with telemetry enabled..."
export DROVER_OTEL_ENABLED=true
export DROVER_OTEL_ENDPOINT=localhost:4317
export DROVER_ENV=development

# Mock claude for testing
export PATH="/bin:/usr/bin:$PATH"
mkdir -p "$TEST_DIR/bin"
cat > "$TEST_DIR/bin/claude" << 'EOF'
#!/bin/bash
echo "Simulated Claude execution for task: $3"
sleep 1
echo "Success on attempt 1"
exit 0
EOF
chmod +x "$TEST_DIR/bin/claude"

"$DROVER_DIR/drover" run

# Step 13: Verify telemetry data
echo ""
echo "1️⃣3️⃣  Verifying telemetry data in ClickHouse..."
sleep 2  # Give time for data to be processed

TRACE_COUNT=$(curl -s 'http://localhost:8123/?query=SELECT%20count()%20FROM%20otel_traces' 2>/dev/null || echo "0")

if [ "$TRACE_COUNT" -gt 0 ]; then
    echo -e "${GREEN}✓ Telemetry data received: $TRACE_COUNT traces${NC}"
else
    echo -e "${RED}✗ No telemetry data found${NC}"
    echo ""
    echo "Checking collector logs..."
    $COMPOSE_CMD -f docker-compose.telemetry.yaml logs --tail 20 otel-collector
fi

# Step 14: Show sample data
echo ""
echo "1️⃣4️⃣  Sample telemetry data:"
curl -s 'http://localhost:8123/?query=SELECT%20SpanName%2C%20StatusCode%2C%20count()%20FROM%20otel_traces%20GROUP%20BY%20SpanName%2C%20StatusCode%20ORDER%20BY%20count()%20DESC' 2>/dev/null || echo "Query failed"

# Summary
echo ""
echo "================================"
echo "📊 Test Summary"
echo "================================"
echo ""
echo "Services running:"
$COMPOSE_CMD -f "$DROVER_DIR/docker-compose.telemetry.yaml" ps
echo ""
echo "Grafana Dashboard: http://localhost:3000 (admin/admin)"
echo "ClickHouse: http://localhost:8123"
echo ""
echo "To stop the stack:"
echo "  $COMPOSE_CMD -f $DROVER_DIR/docker-compose.telemetry.yaml down"
echo ""
echo "To clean up (delete all data):"
echo "  $COMPOSE_CMD -f $DROVER_DIR/docker-compose.telemetry.yaml down -v"
echo "  rm -rf $TEST_DIR"
echo ""
