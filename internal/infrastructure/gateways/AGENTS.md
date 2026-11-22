# Gateways: model wire rules

- Anthropic-Messages endpoints (reached via OpenAI-compatible routes) reject a `name` on any message
  -> `AgentMessage.Name` stays in the core only; `openai_agent.go` never sends it. The tool answer is
  tied to its call by `tool_call_id`.
- The core still needs the tool name for the elided-result note (`agents/loop.go`) -> do not clear
  `Name` in the core to satisfy the wire.
