# Integration Test Plan

## Test Scenarios

### 1. Unit Tests (existing)
- All existing unit tests pass: `make all`

### 2. Smoke Tests (implemented)
- `TestSmokeBeadsPiWorkflow` - Tests beads tracker + pi agent configuration
- `TestSmokeLinearCodexWorkflow` - Tests linear tracker + codex agent configuration

### 3. Integration Test Requirements

To run a real integration test, you need:

#### For Beads + Pi:
- `bd` CLI installed and reachable
- `pi` CLI installed (with Pi account)
- Test project with `WORKFLOW.md` using beads tracker

#### For Linear + Codex:
- `LINEAR_API_KEY` environment variable
- `codex` CLI installed
- Linear project with issues in `Todo` state

### 4. Workflow Files

Test workflow files created at:
- `go/testdata/workflows/beads-pi-smoke.md`
- `go/testdata/workflows/linear-codex-smoke.md`

### 5. Running Tests

```bash
# Unit tests
make all

# Integration tests (with real services)
# Set LINEAR_API_KEY for Linear tests
# bd and pi must be in PATH
go test -v ./internal/app
```

### 6. Test Architecture

The tests use fake implementations:
- `trackerfake.Tracker` - Returns empty candidate list
- `agentfake.Runner` - Simulates instant completion

This allows testing configuration loading and orchestrator initialization
without requiring real external services.