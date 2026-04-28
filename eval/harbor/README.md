# Crest Harbor Adapter

Harbor installed-agent adapter for running Crest's native coding agent on [terminal-bench 2.0](https://tbench.ai).

## Prerequisites

- [Harbor](https://www.harborframework.com/) installed
- Docker running
- `ANTHROPIC_API_KEY` set (or your provider's API key)

## Usage

```bash
# Run with terminal-bench 2.0
harbor run \
  -d terminal-bench/terminal-bench-2 \
  --agent-import-path eval.harbor.crest_agent:CrestAgent \
  -m anthropic/claude-sonnet-4-20250514 \
  -n 8

# Single task for testing
harbor run \
  -d terminal-bench/terminal-bench-2 \
  --agent-import-path eval.harbor.crest_agent:CrestAgent \
  -m anthropic/claude-sonnet-4-20250514 \
  -n 1

# Oracle baseline
harbor run -d terminal-bench/terminal-bench-2 -a oracle
```

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `ANTHROPIC_API_KEY` | API key for the AI provider | (required) |
| `HARBOR_MODEL` | Model to use | `anthropic/claude-sonnet-4-20250514` |
| `CREST_API_TYPE` | API type (`openai-chat`, `anthropic-messages`) | `openai-chat` |
| `CREST_BASE_URL` | Custom API base URL | (provider default) |

## How It Works

1. **Install**: Clones Crest, builds `wavesrv` (the Go backend) in the container
2. **Run**: Starts `wavesrv`, POSTs the task instruction to `/api/post-agent-message`, reads the SSE response stream
3. **Post-run**: Parses the SSE log into an ATIF trajectory for scoring
