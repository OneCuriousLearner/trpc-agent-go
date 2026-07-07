#!/usr/bin/env bash
# scripts/setup-benchmark-fork.sh
#
# Switch the benchmark git submodule to a fork that carries a go.work pinning
# trpc-agent-go to the local working tree. After running this, `cd
# benchmark/<impl> && go build .` resolves trpc-agent-go to the sibling
# checkout (../) instead of a stale external snapshot, so local framework
# changes are exercised by benchmarks without per-run go.mod surgery.
#
# Run this once after cloning the main repo. Idempotent: re-running just
# re-syncs the fork branch.
#
# Usage:
#   ./scripts/setup-benchmark-fork.sh
#   BENCHMARK_FORK_URL=git@github.com:.../trpc-agent-go-benchmark.git ./scripts/setup-benchmark-fork.sh

set -euo pipefail

# Default fork: carries go.work on branch feat/local-workspace.
# Override with BENCHMARK_FORK_URL if you maintain your own fork.
DEFAULT_FORK_URL="https://github.com/OneCuriousLearner/trpc-agent-go-benchmark.git"
FORK_URL="${BENCHMARK_FORK_URL:-$DEFAULT_FORK_URL}"
FORK_BRANCH="${BENCHMARK_FORK_BRANCH:-feat/local-workspace}"

# Resolve repo root (this script lives in scripts/).
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BENCH_DIR="$REPO_ROOT/benchmark"

if [ ! -d "$BENCH_DIR/.git" ] && [ ! -f "$BENCH_DIR/.git" ]; then
    echo "==> benchmark submodule not initialized; running git submodule update --init"
    git -C "$REPO_ROOT" submodule update --init benchmark
fi

echo "==> Pointing benchmark submodule at fork: $FORK_URL (branch $FORK_BRANCH)"
git -C "$BENCH_DIR" remote set-url origin "$FORK_URL"
git -C "$BENCH_DIR" fetch origin "$FORK_BRANCH"
git -C "$BENCH_DIR" checkout -B "$FORK_BRANCH" "origin/$FORK_BRANCH"

echo
echo "==> Done. Verify with:"
echo "    cd benchmark/memory/trpc-agent-go-impl && go list -m trpc.group/trpc-go/trpc-agent-go"
echo "  (should show  => ../, i.e. the local working tree)"
