# Harbor

Runs detent on [Harbor](https://github.com/harbor-framework/harbor) tasks,
such as Terminal-Bench 2.0. The adapter builds detent from this checkout for
the task container's arch, copies it in, and runs it headless in host mode
with `-approve-all`, since the container is thrown away afterwards.

```sh
uv tool install harbor
export DETENT_API_KEY=...        # or OPENROUTER_API_KEY
cd ~/somewhere-under-home
PYTHONPATH=/path/to/detent harbor run -d terminal-bench@2.0 \
  --agent bench.harbor.detent_agent:Detent \
  -m openrouter/openai/gpt-6-luna --n-concurrent 4
```

- Needs Go on the host, for the build.
- On colima, run from under `$HOME`. Only that is shared with the VM, so a
  jobs dir in `/tmp` loses the verifier's output and every trial fails with
  `RewardFileNotFoundError`.
- `--ak steps=200` raises detent's step bound. `DETENT_BASE_URL` points it at
  another endpoint, and an `openrouter/` prefix on the model is dropped.
- detent's log and output land in each trial's `agent/` directory, and token
  counts are read from the log.
- For a baseline, run `-a terminus-2` with the same model.
