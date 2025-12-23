#!/bin/bash
# E2E test script using Kind (Kubernetes in Docker)
# This script manages Kind cluster lifecycle and runs e2e tests

set -e

CLUSTER_NAME="mk8s-e2e-test"
CENTRAL_PORT=8080
GRPC_PORT=9090
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
BIN_DIR="${PROJECT_ROOT}/bin"
CONFIG_DIR="${PROJECT_ROOT}/tests/e2e/config"
LOG_DIR="${PROJECT_ROOT}/tests/e2e/logs"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

check_dependencies() {
    log_info "Checking dependencies..."

    if ! command -v kind &> /dev/null; then
        log_error "kind is not installed. Install it from https://kind.sigs.k8s.io/"
        exit 1
    fi

    if ! command -v kubectl &> /dev/null; then
        log_error "kubectl is not installed."
        exit 1
    fi

    if ! command -v docker &> /dev/null; then
        log_error "docker is not installed."
        exit 1
    fi

    log_info "All dependencies found."
}

create_cluster() {
    log_info "Creating Kind cluster: ${CLUSTER_NAME}..."

    # Check if cluster already exists
    if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
        log_warn "Cluster ${CLUSTER_NAME} already exists. Deleting it first..."
        kind delete cluster --name "${CLUSTER_NAME}"
    fi

    # Create Kind cluster with config
    cat <<EOF | kind create cluster --name "${CLUSTER_NAME}" --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
EOF

    log_info "Kind cluster created successfully."

    # Wait for cluster to be ready
    log_info "Waiting for cluster to be ready..."
    kubectl wait --for=condition=Ready nodes --all --timeout=120s

    log_info "Cluster is ready."
}

delete_cluster() {
    log_info "Deleting Kind cluster: ${CLUSTER_NAME}..."

    if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
        kind delete cluster --name "${CLUSTER_NAME}"
        log_info "Cluster deleted."
    else
        log_warn "Cluster ${CLUSTER_NAME} does not exist."
    fi
}

build_binaries() {
    log_info "Building binaries..."
    cd "${PROJECT_ROOT}"
    make build
    log_info "Binaries built successfully."
}

start_central() {
    log_info "Starting central service..."

    mkdir -p "${LOG_DIR}"
    mkdir -p "${CONFIG_DIR}"

    # Create central config
    cat > "${CONFIG_DIR}/central.yaml" <<EOF
server:
  http_port: ${CENTRAL_PORT}
  grpc_port: ${GRPC_PORT}
EOF

    # Start central service in background
    "${BIN_DIR}/mk8s-central" --config "${CONFIG_DIR}/central.yaml" > "${LOG_DIR}/central.log" 2>&1 &
    CENTRAL_PID=$!
    echo "${CENTRAL_PID}" > "${LOG_DIR}/central.pid"

    # Wait for central to be ready
    log_info "Waiting for central service to be ready..."
    for i in {1..30}; do
        if curl -s "http://localhost:${CENTRAL_PORT}/health" > /dev/null 2>&1; then
            log_info "Central service is ready (PID: ${CENTRAL_PID})."
            return 0
        fi
        sleep 1
    done

    log_error "Central service failed to start. Check ${LOG_DIR}/central.log"
    cat "${LOG_DIR}/central.log"
    exit 1
}

stop_central() {
    log_info "Stopping central service..."

    if [ -f "${LOG_DIR}/central.pid" ]; then
        PID=$(cat "${LOG_DIR}/central.pid")
        if kill -0 "${PID}" 2>/dev/null; then
            kill "${PID}" 2>/dev/null || true
            sleep 1
            kill -9 "${PID}" 2>/dev/null || true
            log_info "Central service stopped."
        fi
        rm -f "${LOG_DIR}/central.pid"
    fi
}

start_agent() {
    log_info "Starting agent..."

    mkdir -p "${LOG_DIR}"

    # Create agent config pointing to local central
    cat > "${CONFIG_DIR}/agent.yaml" <<EOF
central:
  url: "localhost:${GRPC_PORT}"
  token: "dev-token"
cluster:
  name: "${CLUSTER_NAME}"
EOF

    # Start agent in background
    "${BIN_DIR}/mk8s-agent" --config "${CONFIG_DIR}/agent.yaml" > "${LOG_DIR}/agent.log" 2>&1 &
    AGENT_PID=$!
    echo "${AGENT_PID}" > "${LOG_DIR}/agent.pid"

    # Wait for agent to register
    log_info "Waiting for agent to register..."
    for i in {1..30}; do
        # Check if agent appears in clusters list
        CLUSTERS=$(curl -s "http://localhost:${CENTRAL_PORT}/api/v1/clusters" 2>/dev/null || echo "{}")
        if echo "${CLUSTERS}" | grep -q "${CLUSTER_NAME}"; then
            log_info "Agent registered successfully (PID: ${AGENT_PID})."
            return 0
        fi
        sleep 1
    done

    log_error "Agent failed to register. Check ${LOG_DIR}/agent.log"
    cat "${LOG_DIR}/agent.log"
    exit 1
}

stop_agent() {
    log_info "Stopping agent..."

    if [ -f "${LOG_DIR}/agent.pid" ]; then
        PID=$(cat "${LOG_DIR}/agent.pid")
        if kill -0 "${PID}" 2>/dev/null; then
            kill "${PID}" 2>/dev/null || true
            sleep 1
            kill -9 "${PID}" 2>/dev/null || true
            log_info "Agent stopped."
        fi
        rm -f "${LOG_DIR}/agent.pid"
    fi
}

setup_cli_config() {
    log_info "Setting up CLI config..."

    # Create CLI config directory
    mkdir -p "${HOME}/.mk8s"

    # Backup existing config if present
    if [ -f "${HOME}/.mk8s/config.yaml" ]; then
        cp "${HOME}/.mk8s/config.yaml" "${HOME}/.mk8s/config.yaml.backup"
    fi

    # Create CLI config for e2e tests
    cat > "${HOME}/.mk8s/config.yaml" <<EOF
central_url: "http://localhost:${CENTRAL_PORT}"
current_cluster: ""
token: ""
EOF

    log_info "CLI config created."
}

restore_cli_config() {
    log_info "Restoring CLI config..."

    if [ -f "${HOME}/.mk8s/config.yaml.backup" ]; then
        mv "${HOME}/.mk8s/config.yaml.backup" "${HOME}/.mk8s/config.yaml"
        log_info "CLI config restored from backup."
    else
        rm -f "${HOME}/.mk8s/config.yaml"
        log_info "CLI config removed (no backup existed)."
    fi
}

cleanup() {
    log_info "Cleaning up..."
    stop_agent
    stop_central
    restore_cli_config
    delete_cluster
    rm -rf "${LOG_DIR}" "${CONFIG_DIR}"
    log_info "Cleanup complete."
}

run_tests() {
    log_info "Running e2e tests..."

    cd "${PROJECT_ROOT}"

    # Run Go e2e tests
    go test -v -tags=e2e ./tests/e2e/... -central-url="http://localhost:${CENTRAL_PORT}" -cluster-name="${CLUSTER_NAME}" -bin-dir="${BIN_DIR}"

    log_info "All e2e tests passed!"
}

# Main execution
main() {
    case "${1:-}" in
        setup)
            check_dependencies
            create_cluster
            build_binaries
            start_central
            start_agent
            setup_cli_config
            log_info "E2E environment is ready!"
            log_info "Central: http://localhost:${CENTRAL_PORT}"
            log_info "Cluster: ${CLUSTER_NAME}"
            # Remove trap so cleanup doesn't run
            trap - EXIT
            ;;
        teardown)
            # Don't trap, just run cleanup
            trap - EXIT
            cleanup
            ;;
        test)
            # Set trap for cleanup on exit
            trap cleanup EXIT
            check_dependencies
            create_cluster
            build_binaries
            start_central
            start_agent
            setup_cli_config
            run_tests
            # cleanup happens via trap
            ;;
        *)
            echo "Usage: $0 {setup|teardown|test}"
            echo ""
            echo "Commands:"
            echo "  setup     - Create Kind cluster and start services (for manual testing)"
            echo "  teardown  - Stop services and delete Kind cluster"
            echo "  test      - Run full e2e test suite"
            exit 1
            ;;
    esac
}

main "$@"
