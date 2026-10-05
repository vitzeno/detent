"""detent as a Harbor installed agent.

Built from this checkout for the task container's arch, copied in, and run
headless in host mode: Harbor's container is the sandbox.
"""

import json
import os
import shlex
import subprocess
import tempfile
from pathlib import Path
from typing import Annotated, override

from pydantic import Field

from harbor.agents.installed.base import BaseInstalledAgent, PackageSpec, with_prompt_template
from harbor.agents.options import Cli, InstalledAgentOptions
from harbor.environments.base import BaseEnvironment
from harbor.models.agent.context import AgentContext

REPO = Path(__file__).resolve().parents[2]
LOG_DIR = "/logs/agent/detent"
GOARCH = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}


class DetentOptions(InstalledAgentOptions):
    steps: Annotated[int | None, Cli("-steps")] = Field(
        default=None, description="Steps per request before detent stops (detent's default when unset)."
    )
    finish_check: bool | None = Field(
        default=None, description="Ask the model to check its work before finishing (detent's default, on, when unset)."
    )


class Detent(BaseInstalledAgent):
    options_model = DetentOptions
    options: DetentOptions

    # What detent's own tools shell out to beyond coreutils.
    SYSTEM_PACKAGES = {
        **BaseInstalledAgent.SYSTEM_PACKAGES,
        "perl": PackageSpec.standard("perl"),
        "diff": PackageSpec(
            commands=("diff",),
            packages={"apt-get": ("diffutils",), "dnf": ("diffutils",), "yum": ("diffutils",), "apk": ("diffutils",)},
        ),
    }

    @staticmethod
    @override
    def name() -> str:
        return "detent"

    @override
    def get_version_command(self) -> str | None:
        return "detent -version"

    @override
    def parse_version(self, stdout: str) -> str:
        return stdout.split()[-1]

    @override
    async def install(self, environment: BaseEnvironment) -> None:
        await self._ensure_tools(environment)
        machine = (await environment.exec(command="uname -m")).stdout.strip()
        arch = GOARCH.get(machine)
        if arch is None:
            raise RuntimeError(f"no detent build for {machine!r}")
        with tempfile.TemporaryDirectory(prefix="detent-") as tmp:
            binary = Path(tmp) / "detent"
            subprocess.run(
                ["go", "build", "-o", str(binary), "./cmd/detent"],
                cwd=REPO, check=True,
                env={**os.environ, "GOOS": "linux", "GOARCH": arch, "CGO_ENABLED": "0"},
            )
            await environment.upload_file(binary, "/installed-agent/detent")
        await self.exec_as_root(environment, command="install -m 755 /installed-agent/detent /usr/local/bin/detent")

    async def _ensure_tools(self, environment: BaseEnvironment) -> None:
        # Only what is missing, and never fatal: an image with stale apt lists
        # 404s on install, and detent runs without these, just less well.
        want = [name for name, check in (
            ("perl", "command -v perl"),
            ("diff", "command -v diff"),
            ("ca_certificates", "test -s /etc/ssl/certs/ca-certificates.crt"),
        ) if (await environment.exec(command=check)).return_code != 0]
        if not want:
            return
        try:
            await self.ensure_system_dependencies(environment, tuple(want))
        except Exception as exc:
            self.logger.warning("could not install %s, running without: %s", ", ".join(want), exc)

    def _model_env(self) -> dict[str, str]:
        env = {"DETENT_LOG_DIR": LOG_DIR, "DETENT_LOG_BODIES": "1"}
        key = self._get_env("DETENT_API_KEY", "OPENROUTER_API_KEY", "OPENAI_API_KEY")
        if key:
            env["DETENT_API_KEY"] = key
        if self.options.finish_check is not None:
            env["DETENT_FINISH_CHECK"] = str(self.options.finish_check).lower()
        if url := self._get_env("DETENT_BASE_URL"):
            env["DETENT_BASE_URL"] = url
        if self.model_name:
            # Harbor names models provider/model. detent speaks to one endpoint, so
            # an openrouter/ prefix is Harbor's and the rest is the endpoint's.
            env["DETENT_MODEL"] = self.model_name.removeprefix("openrouter/")
        return env

    @override
    @with_prompt_template
    async def run(self, instruction: str, environment: BaseEnvironment, context: AgentContext) -> None:
        flags = self.build_cli_flags()
        await self.exec_as_agent(
            environment,
            command=(
                f"mkdir -p {LOG_DIR} && "
                f"detent -sandbox host -approve-all {flags} -prompt {shlex.quote(instruction)} "
                "2>&1 </dev/null | tee /logs/agent/detent.txt"
            ),
            env=self._model_env(),
        )

    @override
    def populate_context_post_run(self, context: AgentContext) -> None:
        prompt = completion = cached = 0
        cost = None
        for log in (self.logs_dir / "detent").glob("*.jsonl"):
            for line in log.read_text().splitlines():
                try:
                    rec = json.loads(line)
                except json.JSONDecodeError:
                    continue
                if rec.get("event") == "step.ended":
                    prompt += rec.get("prompt_tokens", 0)
                    completion += rec.get("completion_tokens", 0)
                    cached += rec.get("cached_tokens", 0)
                    # Absent when the endpoint sent no cost, which is unknown, not free.
                    if "cost" in rec:
                        cost = (cost or 0) + rec["cost"]
        if prompt or completion:
            context.n_input_tokens = prompt
            context.n_output_tokens = completion
            context.n_cache_tokens = cached
        if cost is not None:
            context.cost_usd = cost
