# Copyright 2026, Command Line Inc.
# SPDX-License-Identifier: Apache-2.0

"""
Harbor installed-agent adapter for Crest's native coding agent.

Crest is a terminal application with an embedded coding agent. This adapter
installs the Crest server (`wavesrv`) in a Docker container and runs the
agent via its HTTP API (POST /api/post-agent-message), reading the SSE
response stream.

Usage:
    harbor run \\
        -d terminal-bench/terminal-bench-2 \\
        --agent-import-path eval.harbor.crest_agent:CrestAgent \\
        -m anthropic/claude-sonnet-4-20250514 \\
        -n 8
"""

import json
import os
import shlex
import uuid

from harbor.agents.installed.base import BaseInstalledAgent, with_prompt_template
from harbor.environments.base import BaseEnvironment
from harbor.models.agent.context import AgentContext

CREST_REPO = "https://github.com/s-zx/crest.git"
CREST_BRANCH = "feat/native-agent"

WAVESRV_PORT = 1819
AGENT_ENDPOINT = f"http://localhost:{WAVESRV_PORT}/api/post-agent-message"


class CrestAgent(BaseInstalledAgent):
    """Harbor adapter that installs and runs Crest's native coding agent."""

    @staticmethod
    def name() -> str:
        return "crest"

    def version(self) -> str | None:
        return "0.1.0"

    async def install(self, environment: BaseEnvironment) -> None:
        await self.exec_as_root(
            environment,
            command=(
                "apt-get update && "
                "apt-get install -y --no-install-recommends git curl build-essential && "
                "curl -fsSL https://go.dev/dl/go1.25.6.linux-amd64.tar.gz | tar -C /usr/local -xzf - && "
                "ln -sf /usr/local/go/bin/go /usr/local/bin/go"
            ),
        )

        await self.exec_as_agent(
            environment,
            command=(
                f"git clone --depth=1 --branch {CREST_BRANCH} {CREST_REPO} /tmp/crest && "
                "cd /tmp/crest && "
                "go build -o /usr/local/bin/wavesrv ./cmd/server && "
                "rm -rf /tmp/crest"
            ),
        )

        await self.exec_as_agent(
            environment,
            command="wavesrv --version || echo 'wavesrv installed'",
        )

    @with_prompt_template
    async def run(
        self,
        instruction: str,
        environment: BaseEnvironment,
        context: AgentContext,
    ) -> None:
        model = os.environ.get("HARBOR_MODEL", "anthropic/claude-sonnet-4-20250514")
        api_key = os.environ.get("ANTHROPIC_API_KEY", "") or os.environ.get("OPENROUTER_API_KEY", "")
        api_type = os.environ.get("CREST_API_TYPE", "openai-chat")
        base_url = os.environ.get("CREST_BASE_URL", "")

        env_vars = {
            "WAVETERM_DEV": "1",
        }
        env_export = " ".join(f"{k}={shlex.quote(v)}" for k, v in env_vars.items())

        settings_json = json.dumps({
            "ai:apitype": api_type,
            "ai:baseurl": base_url,
            "ai:apitoken": api_key,
            "ai:model": model,
        })

        chat_id = str(uuid.uuid4())
        message_id = str(uuid.uuid4())

        request_body = json.dumps({
            "chatid": chat_id,
            "tabid": "eval-tab",
            "blockid": "eval-block",
            "mode": "do",
            "aimode": "",
            "msg": {
                "messageid": message_id,
                "parts": [{"type": "text", "text": instruction}],
            },
            "context": {
                "cwd": os.environ.get("HARBOR_TASK_DIR", "/home/agent"),
            },
        })

        setup_cmd = (
            f"mkdir -p ~/.config/waveterm && "
            f"echo {shlex.quote(settings_json)} > ~/.config/waveterm/settings.json"
        )

        agent_cmd = (
            f"{env_export} wavesrv &"
            f" WAVESRV_PID=$! && "
            f"sleep 2 && "
            f"curl -s -N -X POST {AGENT_ENDPOINT} "
            f"-H 'Content-Type: application/json' "
            f"-d {shlex.quote(request_body)} "
            f"| tee /logs/agent/crest-agent.txt && "
            f"kill $WAVESRV_PID 2>/dev/null || true"
        )

        await self.exec_as_agent(environment, command=setup_cmd)
        await self.exec_as_agent(environment, command=agent_cmd)

    def populate_context_post_run(self, context: AgentContext) -> None:
        log_path = "/logs/agent/crest-agent.txt"
        if not os.path.exists(log_path):
            return

        trajectory = []
        try:
            with open(log_path) as f:
                for line in f:
                    line = line.strip()
                    if not line or not line.startswith("data:"):
                        continue
                    data_str = line[len("data:"):].strip()
                    if not data_str:
                        continue
                    try:
                        event = json.loads(data_str)
                        trajectory.append(event)
                    except json.JSONDecodeError:
                        continue
        except Exception:
            pass

        if trajectory:
            trajectory_path = "/logs/agent/trajectory.json"
            with open(trajectory_path, "w") as f:
                json.dump(
                    {"schema": "atif-v1.2", "events": trajectory},
                    f,
                    indent=2,
                )
