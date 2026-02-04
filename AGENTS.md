# Working with Jules
Use the Jules MCP Server to delegate tasks to Jules.

## Setting Jules Session source
Use this GitHub repo source for every Jules session: mmulet/pupapppupps
Unless specified use main as the base branch.

## Monitoring changes from Jules
Use the `get_session_state` tool to get the status of a Jules session. When a session is busy, Jules is working. When a session is stable Jules is idle and not making changes.

## Reviewing Jules Code
Use the `get_code_review_context` tool to get a high level context of the changes Jules made. Use `show_diff` to get the individual code changes.
