# Playbook

This crew's teams and routing rules. The orchestrator reads this file at the start of every session.

## Teams

| Team | Members | Use for |
|---|---|---|
| dev | engineer, reviewer | features and fixes in a project (`cadrei up dev <project>`) |

## Pipelines

- **Feature (dev):** engineer builds it on a branch, then reviewer reviews the branch, then engineer fixes the findings.

The user adds teams and members by asking the orchestrator, which writes `members/<team>/<role>.md` and updates this playbook.
