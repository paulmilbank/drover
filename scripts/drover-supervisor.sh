#!/bin/bash
# drover-supervisor.sh - Supervisor script for Drover with auto-restart
#
# This supervisor runs drover in a loop with memory limits.
# When drover exits (whether normally, OOM-killed, or crashed),
# it will be restarted automatically.

set -e

# Configuration
MEMORY_LIMIT="${DROVER_MEMORY_LIMIT:-2G}"
RESTART_DELAY="${DROVER_RESTART_DELAY:-5}"
LOG_FILE="${DROVER_SUPERVISOR_LOG:-/var/log/drover-supervisor.log}"

# Command to run (default: drover run)
DROVER_CMD="${DROVER_CMD:-drover run}"
DROVER_CMD_ARGS="${@}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*" | tee -a "$LOG_FILE"
}

log_success() {
    echo -e "${GREEN}[$(date '+%Y-%m-%d %H:%M:%S')] $*${NC}" | tee -a "$LOG_FILE"
}

log_warning() {
    echo -e "${YELLOW}[$(date '+%Y-%m-%d %H:%M:%S')] $*${NC}" | tee -a "$LOG_FILE"
}

log_error() {
    echo -e "${RED}[$(date '+%Y-%m-%d %H:%M:%S')] $*${NC}" | tee -a "$LOG_FILE"
}

# Check if systemd is available for memory limits
use_systemd() {
    command -v systemd-run >/dev/null 2>&1
}

run_with_systemd() {
    log "Starting drover with systemd-run (memory limit: $MEMORY_LIMIT)"
    systemd-run --scope \
        -p "MemoryMax=$MEMORY_LIMIT" \
        -p "MemoryHigh=$((MEMORY_LIMIT - 512M))" \
        bash -c "$DROVER_CMD $DROVER_CMD_ARGS"
}

run_without_systemd() {
    log "Starting drover (no memory limit enforcement)"
    bash -c "$DROVER_CMD $DROVER_CMD_ARGS"
}

# Main supervisor loop
supervisor_loop() {
    log "=== Drover Supervisor Starting ==="
    log "Memory limit: $MEMORY_LIMIT"
    log "Restart delay: ${RESTART_DELAY}s"
    log "Command: $DROVER_CMD $DROVER_CMD_ARGS"
    log "================================"

    while true; do
        log "Starting drover..."

        # Run drover with appropriate method
        if use_systemd; then
            run_with_systemd
        else
            run_without_systemd
        fi

        EXIT_CODE=$?

        case $EXIT_CODE in
            0)
                log_success "Drover completed successfully (exit code 0)"
                log "=== Supervisor Exiting ==="
                exit 0
                ;;
            137)
                log_error "Drover was OOM-killed (exit code 137)"
                log_warning "Restarting in ${RESTART_DELAY}s..."
                ;;
            130)
                log_error "Drover was SIGTERM'd (exit code 130)"
                log "=== Supervisor Exiting ==="
                exit 0
                ;;
            *)
                log_error "Drover failed with exit code $EXIT_CODE"
                log_warning "Restarting in ${RESTART_DELAY}s..."
                ;;
        esac

        sleep "$RESTART_DELAY"
    done
}

# Handle signals for graceful shutdown
trap 'log "Received shutdown signal, exiting..."; exit 0' SIGTERM SIGINT

# Create log directory if needed
LOG_DIR=$(dirname "$LOG_FILE")
if [ "$LOG_DIR" != "." ] && [ ! -d "$LOG_DIR" ]; then
    mkdir -p "$LOG_DIR" 2>/dev/null || LOG_FILE="/dev/null"
fi

# Run supervisor
supervisor_loop
