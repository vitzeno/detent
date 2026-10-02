# Harbor

Runs detent on [Harbor](https://github.com/harbor-framework/harbor) tasks,
such as Terminal-Bench 2.0. For each trial Harbor starts the task's
container, the adapter builds detent from this checkout and copies it in,
detent works the task headless with `-approve-all`, and the task's own tests
score the container afterwards: 1 for a pass, 0 for anything else.

## Setup

```sh
uv tool install harbor               # puts harbor in ~/.local/bin
mkdir -p ~/.cache/detent-bench       # jobs are written here
```

You also need Go, since the adapter builds detent on every trial.

### colima

Every task container runs in Docker, so on a Mac colima must be up for the
whole run, even though detent itself runs in host mode inside each one.
Stopping it mid-run kills every trial at once.

```sh
colima start --cpu 6 --memory 8 --kubernetes=false   # first time
colima start                                         # after that, same settings
colima stop                                          # once the run is done
```

Each trial takes about a CPU and 2GB, so `-n` across all running jobs should
stay near the VM's CPU count. With colima down, `harbor run` fails at once
with a Docker connection error, so nothing is lost by forgetting.

Always run from under `$HOME`: only that is shared with the VM, so a jobs
dir in `/tmp` loses the test results and every trial fails with
`RewardFileNotFoundError`.

## Running

```sh
cd ~/.cache/detent-bench
export DETENT_API_KEY=sk-or-...      # Harbor does not read .detent.yaml
export PYTHONPATH=~/Documents/Projects/detent/bench/harbor

harbor run -d terminal-bench@2.0 \
  --agent detent:Detent \
  -m openrouter/openai/gpt-6-luna \
  -i fix-git -i overfull-hbox \
  -k 3 -n 6 --job-name detent-try
```

| Flag | Does |
| ---- | ---- |
| `-d terminal-bench@2.0` | the dataset. `harbor datasets list` shows the others |
| `-i <task>` | run only this task, repeatable. Leave out for all 89 |
| `-l 10` | run at most 10 tasks |
| `-k 3` | attempts per task. Models vary, so one attempt says little |
| `-n 6` | trials at once. Each wants a CPU and 2GB or so |
| `--job-name` | the folder under `jobs/`. Defaults to a timestamp |
| `--ak steps=200` | detent's step bound (100 by default) |
| `-q` | less output while it runs |

`DETENT_BASE_URL` points detent at another endpoint, and an `openrouter/`
prefix on the model is dropped before detent sees it.

### A baseline

Run Harbor's own agent on the same model and tasks, so a gap says something
about the harness rather than the model:

```sh
export OPENROUTER_API_KEY=sk-or-...  # terminus-2 reads this one
harbor run -d terminal-bench@2.0 -a terminus-2 -m openrouter/openai/gpt-6-luna \
  -i fix-git -i overfull-hbox -k 3 -n 6 --job-name terminus-try
```

Set each key in its own `export`. `export A=x B="$A"` expands `$A` before
assigning it, which leaves `B` empty and fails every trial on auth.

## Watching a run

```sh
python3 bench/harbor/status.py                # every job under ~/.cache/detent-bench/jobs
python3 bench/harbor/status.py ~/other/jobs   # or somewhere else
```

```
detent-try  [running]  4/6 finished, mean 0.750
  fix-git                          ✓ ✓ ✓
  overfull-hbox                    ✗ … …
  running: overfull-hbox (6m, 31 steps)
```

✓ passed, ✗ failed the tests, ! errored (Harbor or the agent broke, so no
score), … still running. A running trial shows how long it has gone and,
for detent, how many steps it has taken. It only reads files, so run it as
often as you like:

```sh
while true; do clear; python3 bench/harbor/status.py; sleep 30; done
```

## Results

`harbor view jobs` from `~/.cache/detent-bench` opens a local page with
every job, trial, score and log.

Or the files, in `jobs/<job>/`:

```
result.json                    the job: mean, counts, errors
<task>__<id>/
  result.json                  score, tokens, timings, any exception
  verifier/test-stdout.txt     the tests' output, which says why it failed
  agent/detent.txt             what detent printed: each call and its reply
  agent/detent/<session>.jsonl detent's log: every command, output, step and token count
  trial.log                    Harbor's own log
```

For a failure, read `test-stdout.txt` for what the tests expected, then
`detent.txt` for what detent did, then the `.jsonl` for exact output. A
`detent: info: the model stopped with no answer and no call` line means
detent nudged a model that went quiet mid-task.

## Cleaning up

Harbor removes each trial's container but keeps its image, and Terminal-Bench
images average about 2GB, so the medium set alone leaves around 120GB in
colima. colima's disk file only grows, so freeing space inside the VM is not
enough on its own. Once a run is done:

```sh
docker images 'alexgshaw/*' -q | xargs docker rmi    # the Terminal-Bench images
docker builder prune -f                              # build cache
colima ssh -- sudo fstrim -a                         # hand the space back to the Mac
```

Keep the images instead if another run is coming, since pulling them again is
most of a trial's startup. Avoid `docker image prune -a` unless you want every
unused image gone, other projects' included.
