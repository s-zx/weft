# Copyright 2026, Command Line Inc.
# SPDX-License-Identifier: Apache-2.0

"""
Harbor adapter for Crest's native coding agent.

Usage:
    harbor run -d "terminal-bench@2.0" \
        --agent-import-path benchmarks.crest_agent:CrestAgent \
        -m anthropic/claude-sonnet-4-5 \
        -n 1 --task-name <task>

Requires: crest-headless binary built for linux/amd64.
    cd <repo> && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o benchmarks/crest-headless ./cmd/crest-headless
"""

from __future__ import annotations

import json
import logging
import os
from pathlib import Path

from harbor.agents.installed.base import BaseInstalledAgent, with_prompt_template
from harbor.environments.base import BaseEnvironment
from harbor.models.agent.context import AgentContext

logger = logging.getLogger(__name__)

BINARY_NAME = "crest-headless"
INSTALL_DIR = "/home/agent/.local/bin"


class CrestAgent(BaseInstalledAgent):
    SUPPORTS_ATIF = True

    @staticmethod
    def name() -> str:
        return "crest"

    def _binary_path(self) -> Path:
        candidates = [
            Path(__file__).parent / BINARY_NAME,
            Path(__file__).parent.parent / BINARY_NAME,
        ]
        for p in candidates:
            if p.exists():
                return p
        raise FileNotFoundError(
            f"{BINARY_NAME} not found. Build it first:\n"
            f"  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o benchmarks/{BINARY_NAME} ./cmd/crest-headless"
        )

    async def install(self, environment: BaseEnvironment) -> None:
        binary = self._binary_path()
        logger.info("Installing crest-headless from %s", binary)

        await self.exec_as_root(environment, command=f"mkdir -p {INSTALL_DIR}")

        await environment.copy_to_container(
            str(binary),
            f"{INSTALL_DIR}/{BINARY_NAME}",
        )
        await self.exec_as_root(
            environment,
            command=f"chmod +x {INSTALL_DIR}/{BINARY_NAME}",
        )

        result = await self.exec_as_agent(
            environment,
            command=f"{INSTALL_DIR}/{BINARY_NAME} --version || echo 'crest-headless installed'",
        )
        logger.info("Install check: %s", result.stdout.strip())

    @with_prompt_template
    async def run(
        self,
        instruction: str,
        environment: BaseEnvironment,
        context: AgentContext,
    ) -> None:
        api_key = os.environ.get("ANTHROPIC_API_KEY", "")
        if not api_key:
            raise RuntimeError("ANTHROPIC_API_KEY environment variable is required")

        model = self.model_name or "claude-sonnet-4-5"
        request = json.dumps({
            "prompt": instruction,
            "mode": "bench",
            "model": model,
        })

        env = {
            "ANTHROPIC_API_KEY": api_key,
            "PATH": f"{INSTALL_DIR}:/usr/local/bin:/usr/bin:/bin",
        }

        crest_endpoint = os.environ.get("CREST_API_ENDPOINT", "")
        if crest_endpoint:
            env["CREST_API_ENDPOINT"] = crest_endpoint

        cmd = f"echo {_shell_quote(request)} | {INSTALL_DIR}/{BINARY_NAME}"

        logger.info("Running crest agent with model=%s", model)

        result = await self.exec_as_agent(
            environment,
            command=cmd,
            env=env,
            timeout_sec=1800,
        )

        self._parse_output(result.stdout, context)

        if result.return_code != 0:
            logger.warning(
                "crest-headless exited with code %d: %s",
                result.return_code,
                result.stderr[:500] if result.stderr else "(no stderr)",
            )

    def populate_context_post_run(self, context: AgentContext) -> None:
        pass

    def _parse_output(self, stdout: str, context: AgentContext) -> None:
        if not stdout:
            return

        context.metadata = context.metadata or {}
        events = []

        for line in stdout.strip().splitlines():
            line = line.strip()
            if not line:
                continue
            try:
                event = json.loads(line)
                events.append(event)
            except json.JSONDecodeError:
                continue

        context.metadata["events"] = events
        context.metadata["n_events"] = len(events)

        for event in events:
            if event.get("type") == "error":
                context.metadata["error"] = event.get("text", "")


def _shell_quote(s: str) -> str:
    return "'" + s.replace("'", "'\\''") + "'"
