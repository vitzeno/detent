---
name: skill-creator
description: Create or improve an Agent Skill, a folder with a SKILL.md that teaches you one kind of task. Use when the human asks to make, write, add, turn something into, or fix a skill.
---

# Skill creator

A skill is a folder holding a `SKILL.md`: a name and a description up front, then
instructions for one kind of task. Only the name and description are in your
context until a request matches, so the description decides whether the skill
is ever used, and the body is read only once it is.

## 1. Find out what the skill is for

Before writing anything, settle these with the human. Ask only what the request
and the repository do not already answer:

- **The task:** what should it get done, and what does finished look like?
- **The trigger:** what would someone say or be doing when it should load?
- **Where it lives:**
  - the project's `.agents/skills/<name>/` to share it with everyone in the repo;
  - `~/.agents/skills/<name>/` to keep it as the human's own.
- **Who starts it:** the model on its own, or only the human with `/<name>`?

If the human is turning work you just did together into a skill, take the steps,
commands and corrections from this conversation instead of asking again.

## 2. Write SKILL.md

```markdown
---
name: release-notes
description: Draft release notes from the commits since the last tag. Use when asked for release notes, a changelog entry or a summary of what shipped.
---

# Release notes

1. Find the last tag with `git describe --tags --abbrev=0`.
2. ...
```

- **name:** lowercase letters, digits and single hyphens, at most 64
  characters, and the same as the folder's name.
- **description:** what the skill does *and* when to use it, in the words a
  request would use. One or two sentences. Quote it if it contains a colon.
- **Optional keys:**
  - `disable-model-invocation: true` keeps the skill for the human to call
    with `/<name>`;
  - `user-invocable: false` leaves it to the model alone.
- **The body:** imperative steps you can follow without guessing. Name the
  exact commands, files and checks, and say what done looks like. Leave out
  what any capable agent already knows.

## 3. Keep it small

- Keep `SKILL.md` under about 300 lines. Move long reference material into its
  own file in the folder, and say in `SKILL.md` when to read it.
- Put repeatable work in `scripts/` and tell the reader to run it, rather than
  pasting the same code into every use. Scripts run like any other command,
  through the same approvals.
- One skill does one kind of task. Two unrelated jobs are two skills.

## 4. Check it

1. Read the file back. The frontmatter must open and close with `---`, and the
   description must be on one line or quoted.
2. Confirm the folder name matches `name`.
3. Tell the human how to use it: by asking for the task in their own words, or
   with `/<name>` unless it is model only. detent finds skills when it starts,
   so a new one appears in `/skills` after a restart.

## Improving a skill

Load it, ask the human what went wrong or what is missing, and change the
smallest part that fixes it: usually the description, when the skill does not
load when it should, or a step that was vague. Keep its name, so nothing that
uses it breaks.
